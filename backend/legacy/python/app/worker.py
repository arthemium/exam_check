"""Durable PostgreSQL work queue with expiring leases and bounded retries."""

import logging
import signal
import threading
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from uuid import uuid4

from openai import APIConnectionError, APIStatusError, APITimeoutError, RateLimitError
from sqlalchemy import and_, or_, select, update

from app.checking import CheckingError, check_exam
from app.config import get_settings
from app.database import SessionLocal
from app.models import CheckJob, Exam

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class Claim:
    job_id: str
    exam_id: str
    token: str


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


def expired_before() -> datetime:
    return utcnow() - timedelta(seconds=get_settings().job_lease_seconds)


def claim_job() -> Claim | None:
    eligible = or_(
        and_(CheckJob.status == "queued", CheckJob.available_at <= utcnow()),
        and_(CheckJob.status == "processing", CheckJob.locked_at < expired_before()),
    )
    with SessionLocal.begin() as db:
        job = db.scalar(
            select(CheckJob)
            .where(eligible)
            .order_by(CheckJob.available_at, CheckJob.created_at)
            .limit(1)
            .with_for_update(skip_locked=True)
        )
        if job is None:
            return None
        exam = db.get(Exam, job.exam_id)
        if exam is None or job.attempts >= get_settings().job_max_attempts:
            job.status = "failed"
            job.last_error = "Processing stopped after repeated interrupted attempts."
            job.locked_at, job.lease_token = None, None
            if exam is not None:
                exam.status, exam.error = "failed", job.last_error
            return None
        job.status, job.attempts = "processing", job.attempts + 1
        job.locked_at, job.lease_token = utcnow(), uuid4().hex
        exam.status, exam.error = "processing", None
        return Claim(job.id, job.exam_id, job.lease_token)


def owned_job(db, claim: Claim):
    return db.scalar(
        select(CheckJob)
        .where(CheckJob.id == claim.job_id, CheckJob.status == "processing", CheckJob.lease_token == claim.token)
        .with_for_update()
    )


def complete_job(claim: Claim, result: dict) -> bool:
    with SessionLocal.begin() as db:
        job = owned_job(db, claim)
        if job is None:
            return False
        exam = db.get(Exam, claim.exam_id)
        if exam is None:
            return False
        exam.result, exam.status, exam.error = result, "completed", None
        job.status, job.last_error = "completed", None
        job.locked_at, job.lease_token = None, None
        return True


def retryable(error: Exception) -> bool:
    if isinstance(error, CheckingError):
        return error.retryable
    if isinstance(error, (APIConnectionError, APITimeoutError, RateLimitError)):
        return True
    return isinstance(error, APIStatusError) and error.status_code >= 500


def safe_message(error: Exception) -> str:
    if isinstance(error, CheckingError):
        return str(error)
    if isinstance(error, RateLimitError):
        return "The checking provider is busy or its usage allowance was reached."
    if isinstance(error, (APIConnectionError, APITimeoutError)):
        return "The checking provider could not be reached in time."
    if isinstance(error, APIStatusError):
        return "The checking provider rejected the request. Check the server configuration."
    return "Exam processing failed. Check the server configuration and try again."


def fail_job(claim: Claim, error: Exception) -> bool:
    with SessionLocal.begin() as db:
        job = owned_job(db, claim)
        if job is None:
            return False
        will_retry = retryable(error) and job.attempts < get_settings().job_max_attempts
        job.status = "queued" if will_retry else "failed"
        job.last_error = safe_message(error)
        job.available_at = utcnow() + timedelta(seconds=min(30 * 2 ** (job.attempts - 1), 300))
        job.locked_at, job.lease_token = None, None
        exam = db.get(Exam, claim.exam_id)
        if exam is not None:
            exam.status, exam.error = job.status, job.last_error
        return True


def renew_lease(claim: Claim) -> bool:
    with SessionLocal.begin() as db:
        changed = db.execute(
            update(CheckJob)
            .where(CheckJob.id == claim.job_id, CheckJob.status == "processing", CheckJob.lease_token == claim.token)
            .values(locked_at=utcnow())
        )
        return changed.rowcount == 1


class Heartbeat:
    def __init__(self, claim: Claim):
        self.claim = claim
        self.stop = threading.Event()
        self.thread = threading.Thread(target=self.run, daemon=True)

    def run(self) -> None:
        interval = max(1, get_settings().job_lease_seconds / 3)
        while not self.stop.wait(interval):
            try:
                if not renew_lease(self.claim):
                    return
            except Exception:
                logger.warning("Lease renewal failed for job %s", self.claim.job_id)

    def __enter__(self):
        self.thread.start()
        return self

    def __exit__(self, *args):
        self.stop.set()
        self.thread.join(timeout=5)


def process_claim(claim: Claim) -> None:
    with Heartbeat(claim):
        try:
            result = check_exam(claim.exam_id)
            complete_job(claim, result)
        except Exception as error:
            logger.warning("Checking job %s failed (%s)", claim.job_id, type(error).__name__)
            fail_job(claim, error)


def run_once() -> bool:
    claim = claim_job()
    if claim is None:
        return False
    process_claim(claim)
    return True


def main() -> None:
    logging.basicConfig(level=logging.INFO)
    stopping = threading.Event()
    signal.signal(signal.SIGTERM, lambda *_: stopping.set())
    signal.signal(signal.SIGINT, lambda *_: stopping.set())
    while not stopping.is_set():
        try:
            if run_once():
                continue
        except Exception as error:
            logger.error("Worker queue operation failed (%s)", type(error).__name__)
        stopping.wait(get_settings().worker_poll_seconds)


if __name__ == "__main__":
    main()
