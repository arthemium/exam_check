from datetime import timedelta
from typing import Annotated

from fastapi import APIRouter, Depends, File, Form, HTTPException, Query, UploadFile
from fastapi.responses import FileResponse
from pydantic import ValidationError
from sqlalchemy import func, select
from sqlalchemy.orm import Session

from .auth import get_current_user
from .config import get_settings
from .database import get_db
from .folders import owned_folder
from .models import CheckJob, Exam, ExamPage, User, utcnow
from .schemas import CheckType, ExamDetail, ExamInput, ExamOutput, ExamPatch, JobOutput, PageOutput
from .storage import photo_path, remove_photos, save_photo

router = APIRouter(prefix="/api", tags=["exams"])


def owned_exam(db: Session, exam_id: str, owner_id: str, *, lock=False) -> Exam:
    query = select(Exam).where(Exam.id == exam_id, Exam.owner_id == owner_id)
    exam = db.scalar(query.with_for_update() if lock else query)
    if exam is None:
        raise HTTPException(404, "Exam not found")
    return exam


def exam_detail(db: Session, exam: Exam) -> ExamDetail:
    pages = db.scalars(select(ExamPage).where(ExamPage.exam_id == exam.id).order_by(ExamPage.page_number))
    return ExamDetail(**ExamOutput.model_validate(exam).model_dump(), result=exam.result, error=exam.error,
                      pages=[PageOutput(id=page.id, page_number=page.page_number,
                                        url=f"/api/exams/{exam.id}/pages/{page.id}") for page in pages])


def store_exam(db: Session, exam: Exam, photos: list[UploadFile]) -> ExamDetail:
    settings, keys, total = get_settings(), [], 0
    if not 1 <= len(photos) <= settings.max_pages_per_exam:
        raise HTTPException(422, f"Upload 1–{settings.max_pages_per_exam} pages per exam")
    try:
        db.add(exam)
        db.flush()
        for number, photo in enumerate(photos, start=1):
            key, size = save_photo(photo)
            keys.append(key)
            total += size
            if total > settings.max_exam_bytes:
                raise HTTPException(413, "Exam photos exceed the total size limit")
            db.add(ExamPage(exam_id=exam.id, storage_key=key, mime_type="image/jpeg", page_number=number, file_size=size))
        db.commit()
    except Exception:
        db.rollback()
        remove_photos(keys)
        raise
    finally:
        for photo in photos:
            photo.file.close()
    return exam_detail(db, exam)


@router.post("/folders/{folder_id}/exams", response_model=ExamDetail, status_code=201)
def upload_exam(folder_id: str, title: Annotated[str, Form(max_length=120)],
                photos: Annotated[list[UploadFile], File()], student_name: Annotated[str, Form(max_length=120)] = "",
                checks: Annotated[list[CheckType], Form()] = [],
                user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    owned_folder(db, folder_id, user.id)
    try:
        metadata = ExamInput(title=title, student_name=student_name, checks=checks)
    except ValidationError:
        raise HTTPException(422, "Provide a non-empty exam title and valid checking options") from None
    exam = Exam(owner_id=user.id, folder_id=folder_id, **metadata.model_dump())
    return store_exam(db, exam, photos)


@router.get("/folders/{folder_id}/exams", response_model=list[ExamOutput])
def list_exams(folder_id: str, limit: int = Query(50, ge=1, le=100), offset: int = Query(0, ge=0),
               user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    owned_folder(db, folder_id, user.id)
    return db.scalars(select(Exam).where(Exam.folder_id == folder_id, Exam.owner_id == user.id)
                      .order_by(Exam.created_at, Exam.id).limit(limit).offset(offset)).all()


@router.get("/exams/{exam_id}", response_model=ExamDetail)
def get_exam(exam_id: str, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    return exam_detail(db, owned_exam(db, exam_id, user.id))


@router.patch("/exams/{exam_id}", response_model=ExamOutput)
def update_exam(exam_id: str, body: ExamPatch, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    exam = owned_exam(db, exam_id, user.id, lock=True)
    changes = body.model_dump(exclude_unset=True)
    if "checks" in changes:
        if exam.status in {"queued", "processing"}:
            raise HTTPException(409, "Wait for the current check to finish before changing criteria")
        changes["checks"] = list(dict.fromkeys(changes["checks"]))
        exam.status, exam.result, exam.error = "uploaded", None, None
    for name, value in changes.items():
        setattr(exam, name, value)
    db.commit()
    return exam


@router.get("/exams/{exam_id}/pages/{page_id}")
def get_page(exam_id: str, page_id: str, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    owned_exam(db, exam_id, user.id)
    page = db.scalar(select(ExamPage).where(ExamPage.id == page_id, ExamPage.exam_id == exam_id))
    if page is None or not photo_path(page.storage_key).is_file():
        raise HTTPException(404, "Photo not found")
    return FileResponse(photo_path(page.storage_key), media_type=page.mime_type,
                        headers={"Cache-Control": "private, no-store", "X-Content-Type-Options": "nosniff"})


def check_job_limits(db: Session, user_id: str) -> None:
    settings = get_settings()
    query = select(func.count(CheckJob.id)).join(Exam).where(Exam.owner_id == user_id)
    active = db.scalar(query.where(CheckJob.status.in_(["queued", "processing"]))) or 0
    daily = db.scalar(query.where(CheckJob.created_at >= utcnow() - timedelta(days=1))) or 0
    if active >= settings.max_active_jobs_per_user or daily >= settings.max_jobs_per_user_per_day:
        raise HTTPException(429, "Exam checking limit reached; try again later")


@router.post("/exams/{exam_id}/checks", response_model=JobOutput, status_code=202)
def queue_check(exam_id: str, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    db.scalar(select(User).where(User.id == user.id).with_for_update())
    exam = owned_exam(db, exam_id, user.id, lock=True)
    existing = db.scalar(select(CheckJob).where(CheckJob.exam_id == exam.id, CheckJob.status.in_(["queued", "processing"])))
    if existing:
        return existing
    settings = get_settings()
    if not settings.openai_api_key or not settings.openai_model:
        raise HTTPException(503, "Exam checking has not been configured")
    if not exam.checks:
        raise HTTPException(422, "Select at least one checking criterion")
    check_job_limits(db, user.id)
    job = CheckJob(exam_id=exam.id)
    exam.status, exam.error, exam.result = "queued", None, None
    db.add(job)
    db.commit()
    return job
