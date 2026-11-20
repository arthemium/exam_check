from pathlib import Path

from fastapi import Depends, FastAPI, HTTPException, Request
from fastapi.exception_handlers import request_validation_exception_handler
from fastapi.exceptions import RequestValidationError
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import FileResponse, JSONResponse
from sqlalchemy import select, text
from sqlalchemy.exc import SQLAlchemyError
from sqlalchemy.orm import Session
from starlette.staticfiles import StaticFiles

from . import auth, exams, folders
from .config import get_settings
from .database import get_db
from .middleware import RequestGuard
from .models import User


def create_app() -> FastAPI:
    settings = get_settings()
    app = FastAPI(title="Examcheck API", version="0.1.0",
                  docs_url="/docs" if settings.app_env != "production" else None,
                  redoc_url=None)
    app.add_exception_handler(RequestValidationError, validation_error)
    app.add_middleware(RequestGuard)
    app.add_middleware(CORSMiddleware, allow_origins=sorted(settings.allowed_origins),
                       allow_credentials=True, allow_methods=["GET", "POST", "PATCH"],
                       allow_headers=["Content-Type"])
    app.include_router(auth.router)
    app.include_router(folders.router)
    app.include_router(exams.router)
    app.add_api_route("/health/live", live, tags=["health"])
    app.add_api_route("/health/ready", ready, tags=["health"])
    app.add_api_route("/", index, include_in_schema=False)
    static_assets = Path(__file__).resolve().parent.parent / "static" / "assets"
    if static_assets.is_dir():
        app.mount("/assets", StaticFiles(directory=static_assets), name="assets")
    return app


async def validation_error(request: Request, error: RequestValidationError):
    if request.url.path == "/auth/login":
        return JSONResponse({"detail": "Provide a valid username and password."}, status_code=422)
    return await request_validation_exception_handler(request, error)


def live():
    return {"status": "ok"}


def ready(db: Session = Depends(get_db)):
    try:
        db.execute(text("SELECT 1"))
        db.execute(select(User.id).limit(1))
    except SQLAlchemyError:
        raise HTTPException(503, "Database is not ready") from None
    return {"status": "ready"}


def index():
    frontend = Path(__file__).resolve().parent.parent / "static" / "index.html"
    if frontend.is_file():
        return FileResponse(frontend)
    return {"service": "Examcheck API", "login": "/auth/login", "api": "/api/folders"}


app = create_app()
