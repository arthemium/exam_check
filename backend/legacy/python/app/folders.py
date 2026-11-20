from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from .auth import get_current_user
from .database import get_db
from .models import Exam, Folder, User
from .schemas import FolderInput, FolderOutput

router = APIRouter(prefix="/api/folders", tags=["folders"])


def owned_folder(db: Session, folder_id: str, owner_id: str) -> Folder:
    folder = db.scalar(select(Folder).where(Folder.id == folder_id, Folder.owner_id == owner_id))
    if folder is None:
        raise HTTPException(404, "Folder not found")
    return folder


def folder_output(db: Session, folder: Folder) -> FolderOutput:
    count = db.scalar(select(func.count()).select_from(Exam).where(Exam.folder_id == folder.id))
    return FolderOutput(id=folder.id, name=folder.name, exam_count=count or 0, created_at=folder.created_at)


def save_folder(db: Session, folder: Folder) -> FolderOutput:
    db.add(folder)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(409, "A folder with this name already exists") from None
    return folder_output(db, folder)


@router.get("", response_model=list[FolderOutput])
def list_folders(limit: int = Query(50, ge=1, le=100), offset: int = Query(0, ge=0),
                 user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    rows = db.execute(select(Folder, func.count(Exam.id)).outerjoin(Exam, Exam.folder_id == Folder.id)
                      .where(Folder.owner_id == user.id).group_by(Folder.id)
                      .order_by(Folder.created_at, Folder.id).limit(limit).offset(offset))
    return [FolderOutput(id=folder.id, name=folder.name, exam_count=count, created_at=folder.created_at)
            for folder, count in rows]


@router.post("", response_model=FolderOutput, status_code=201)
def create_folder(body: FolderInput, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    return save_folder(db, Folder(owner_id=user.id, name=body.name, name_key=body.name.casefold()))


@router.patch("/{folder_id}", response_model=FolderOutput)
def rename_folder(folder_id: str, body: FolderInput, user: User = Depends(get_current_user), db: Session = Depends(get_db)):
    folder = owned_folder(db, folder_id, user.id)
    folder.name, folder.name_key = body.name, body.name.casefold()
    return save_folder(db, folder)
