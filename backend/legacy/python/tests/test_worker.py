from datetime import timedelta
from types import SimpleNamespace

import pytest
from sqlalchemy import create_engine, select
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from app import worker
from app.checking import CheckingError
from app.database import Base
from app.models import CheckJob, Exam, Folder, User


@pytest.fixture
def worker_db(monkeypatch):
    engine = create_engine("sqlite://", poolclass=StaticPool, connect_args={"check_same_thread": False})
    Base.metadata.create_all(engine)
    sessions = sessionmaker(bind=engine, expire_on_commit=False)
    monkeypatch.setattr(worker, "SessionLocal", sessions)
    monkeypatch.setattr(worker, "get_settings", lambda: SimpleNamespace(job_lease_seconds=60, job_max_attempts=3))
    with sessions.begin() as db:
        user = User(id="user", google_sub="google-user", email="user@example.test", display_name="Teacher")
        folder = Folder(id="folder", owner_id="user", name="Class", name_key="class")
        exam = Exam(id="exam", owner_id="user", folder_id="folder", title="Essay", checks=["grammar"], status="queued")
        db.add_all([user, folder, exam])
        db.flush()
        db.add(CheckJob(id="job", exam_id="exam"))
    yield sessions
    engine.dispose()


def test_claim_and_completion_are_fenced_by_lease_token(worker_db):
    claim = worker.claim_job()
    assert claim and claim.exam_id == "exam"
    assert worker.claim_job() is None
    stale = worker.Claim(claim.job_id, claim.exam_id, "wrong-token")
    assert worker.complete_job(stale, {"bad": True}) is False
    assert worker.complete_job(claim, {"transcript": "student text"}) is True
    with worker_db() as db:
        exam, job = db.get(Exam, "exam"), db.get(CheckJob, "job")
        assert exam.status == job.status == "completed"
        assert exam.result == {"transcript": "student text"}
        assert job.lease_token is None


def test_transient_failure_requeues_with_bounded_retries(worker_db):
    claim = worker.claim_job()
    worker.fail_job(claim, CheckingError("Try again.", retryable=True))
    assert worker.claim_job() is None
    with worker_db.begin() as db:
        job = db.get(CheckJob, "job")
        assert job.status == "queued" and job.attempts == 1
        job.available_at = worker.utcnow() - timedelta(seconds=1)
        job.attempts = 2
    claim = worker.claim_job()
    worker.fail_job(claim, CheckingError("Try again.", retryable=True))
    with worker_db() as db:
        assert db.get(CheckJob, "job").status == "failed"
        assert db.get(Exam, "exam").error == "Try again."


def test_expired_lease_is_reclaimed_and_old_worker_cannot_write(worker_db):
    first = worker.claim_job()
    with worker_db.begin() as db:
        db.get(CheckJob, "job").locked_at = worker.utcnow() - timedelta(seconds=61)
    second = worker.claim_job()
    assert second and second.token != first.token
    assert worker.renew_lease(first) is False
    assert worker.fail_job(first, CheckingError("stale")) is False
    assert worker.complete_job(second, {"ok": True}) is True


def test_final_interrupted_attempt_does_not_loop_forever(worker_db):
    with worker_db.begin() as db:
        job = db.get(CheckJob, "job")
        job.status, job.attempts = "processing", 3
        job.locked_at = worker.utcnow() - timedelta(seconds=61)
    assert worker.claim_job() is None
    with worker_db() as db:
        assert db.get(Exam, "exam").status == "failed"


def test_permanent_error_is_not_retried_or_leaked(worker_db):
    claim = worker.claim_job()
    worker.fail_job(claim, RuntimeError("secret upstream response"))
    with worker_db() as db:
        job = db.scalar(select(CheckJob))
        assert job.status == "failed"
        assert "secret" not in job.last_error
