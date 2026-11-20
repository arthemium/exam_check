from functools import lru_cache
from pathlib import Path
from typing import Literal
from urllib.parse import urlsplit

from pydantic import Field, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict


def validate_origin(value: str) -> str:
    value = value.rstrip("/")
    origin = urlsplit(value)
    try:
        origin.port
    except ValueError:
        raise ValueError("Origins must use a valid port") from None
    if (origin.scheme not in {"http", "https"} or not origin.hostname or origin.path
            or origin.username is not None or origin.password is not None
            or "?" in value or "#" in value or any(char.isspace() for char in value)):
        raise ValueError("Use an HTTP(S) origin without credentials, a path, query, or fragment")
    return value


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_file=".env", extra="ignore")

    app_env: Literal["development", "production", "test"] = "development"
    database_url: str = "postgresql+psycopg://examcheck@db/examcheck"
    public_base_url: str = "http://localhost:8000"
    frontend_origins: list[str] = Field(default_factory=list)
    cookie_secure: bool = False
    session_days: int = Field(default=7, ge=1, le=30)
    login_username: str = Field(default="", max_length=120)
    login_password: str = Field(default="", max_length=1024, repr=False)
    upload_dir: Path = Path("data/uploads")
    max_page_bytes: int = Field(default=10 * 1024 * 1024, gt=0)
    max_pages_per_exam: int = Field(default=10, ge=1, le=20)
    max_exam_bytes: int = Field(default=20 * 1024 * 1024, gt=0)
    max_request_bytes: int = Field(default=25 * 1024 * 1024, gt=0)
    max_jobs_per_user_per_day: int = Field(default=50, ge=1)
    max_active_jobs_per_user: int = Field(default=2, ge=1)
    openai_api_key: str = ""
    openai_model: str = ""
    openai_timeout_seconds: float = Field(default=90, gt=0, le=300)
    worker_poll_seconds: float = Field(default=2, gt=0)
    job_max_attempts: int = Field(default=3, ge=1, le=5)
    job_lease_seconds: int = Field(default=300, ge=60)

    @model_validator(mode="after")
    def validate_deployment(self):
        self.public_base_url = validate_origin(self.public_base_url)
        self.frontend_origins = [validate_origin(value) for value in self.frontend_origins]
        origin = urlsplit(self.public_base_url)
        if self.app_env == "production":
            if not self.cookie_secure or origin.scheme != "https":
                raise ValueError("Production requires HTTPS and COOKIE_SECURE=true")
        return self

    @property
    def allowed_origins(self) -> set[str]:
        return {self.public_base_url, *self.frontend_origins}


@lru_cache
def get_settings() -> Settings:
    return Settings()
