"""One configured account with revocable, server-side browser sessions."""

from datetime import UTC, datetime, timedelta
from hashlib import sha256
from hmac import compare_digest, new as hmac_new
from secrets import token_urlsafe
from typing import Annotated

from fastapi import APIRouter, Depends, HTTPException, Request, Response
from pydantic import BaseModel, ConfigDict, Field, SecretStr
from sqlalchemy import delete, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.config import get_settings
from app.database import get_db
from app.login_throttle import LoginThrottle
from app.models import LoginSession, User

router = APIRouter(prefix="/auth", tags=["authentication"])
SESSION_COOKIE = "exam_session"
LOCAL_SUBJECT = "local-owner"
Database = Annotated[Session, Depends(get_db)]
login_throttle = LoginThrottle()


class LoginInput(BaseModel):
    model_config = ConfigDict(extra="forbid")

    username: str = Field(min_length=1, max_length=120)
    password: SecretStr = Field(min_length=1, max_length=1024)


class UserProfile(BaseModel):
    id: str
    username: str
    display_name: str


def session_digest(token: str) -> str:
    settings = get_settings()
    message = f"{settings.login_username}\0{token}".encode("utf-8")
    return hmac_new(settings.login_password.encode("utf-8"), message, sha256).hexdigest()


def require_login_configuration() -> None:
    settings = get_settings()
    if not settings.login_username or not settings.login_password:
        raise HTTPException(503, "Login has not been configured.")


def verify_credentials(credentials: LoginInput) -> None:
    settings = get_settings()
    username_matches = compare_digest(credentials.username.encode("utf-8"), settings.login_username.encode("utf-8"))
    password_matches = compare_digest(credentials.password.get_secret_value().encode("utf-8"), settings.login_password.encode("utf-8"))
    if not (username_matches and password_matches):
        raise HTTPException(401, "Invalid username or password.")


def utc_datetime(value: datetime) -> datetime:
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value.astimezone(UTC)


def get_current_user(request: Request, db: Database) -> User:
    require_login_configuration()
    token = request.cookies.get(SESSION_COOKIE, "")
    if not token or len(token) > 256:
        raise HTTPException(401, "Sign in to continue.")
    session = db.get(LoginSession, session_digest(token))
    if session is None:
        raise HTTPException(401, "Sign in to continue.")
    if utc_datetime(session.expires_at) <= datetime.now(UTC):
        db.delete(session)
        db.commit()
        raise HTTPException(401, "Your session has expired. Sign in again.")
    user = db.get(User, session.user_id)
    if user is None or user.google_sub != LOCAL_SUBJECT:
        raise HTTPException(403, "This account does not have access.")
    return user


def local_user(db: Session) -> User:
    user = db.scalar(select(User).where(User.google_sub == LOCAL_SUBJECT))
    if user is None:
        # Reuse existing columns so switching authentication needs no schema migration.
        user = User(google_sub=LOCAL_SUBJECT, email="owner@examcheck.invalid", display_name=get_settings().login_username)
        db.add(user)
        try:
            db.flush()
        except IntegrityError:
            db.rollback()
            user = db.scalar(select(User).where(User.google_sub == LOCAL_SUBJECT))
            if user is None:
                raise HTTPException(409, "Sign-in could not be completed. Try again.") from None
    user.display_name = get_settings().login_username
    return user


def create_session(db: Session, user: User, previous_token: str | None) -> str:
    now = datetime.now(UTC)
    db.execute(delete(LoginSession).where(LoginSession.expires_at <= now))
    if previous_token:
        db.execute(delete(LoginSession).where(LoginSession.id == session_digest(previous_token)))
    token = token_urlsafe(32)
    db.add(LoginSession(id=session_digest(token), user_id=user.id,
                        expires_at=now + timedelta(days=get_settings().session_days)))
    db.commit()
    return token


def set_session_cookie(response: Response, token: str) -> None:
    settings = get_settings()
    response.headers["Cache-Control"] = "no-store"
    response.set_cookie(SESSION_COOKIE, token, max_age=settings.session_days * 86400,
                        httponly=True, secure=settings.cookie_secure, samesite="lax", path="/")


def user_profile(user: User) -> UserProfile:
    return UserProfile(id=user.id, username=get_settings().login_username, display_name=user.display_name)


@router.post("/login", response_model=UserProfile)
def login(credentials: LoginInput, request: Request, response: Response, db: Database):
    require_login_configuration()
    login_throttle.check()
    verify_credentials(credentials)
    user = local_user(db)
    token = create_session(db, user, request.cookies.get(SESSION_COOKIE))
    login_throttle.clear()
    set_session_cookie(response, token)
    return user_profile(user)


@router.get("/me", response_model=UserProfile)
def current_profile(response: Response, user: Annotated[User, Depends(get_current_user)]):
    response.headers["Cache-Control"] = "no-store"
    return user_profile(user)


@router.post("/logout", status_code=204)
def logout(request: Request, db: Database) -> Response:
    token = request.cookies.get(SESSION_COOKIE)
    if token:
        db.execute(delete(LoginSession).where(LoginSession.id == session_digest(token)))
        db.commit()
    response = Response(status_code=204, headers={"Cache-Control": "no-store"})
    response.delete_cookie(SESSION_COOKIE, path="/", secure=get_settings().cookie_secure,
                           httponly=True, samesite="lax")
    return response
