from datetime import UTC, datetime, timedelta
from types import SimpleNamespace

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, select
from sqlalchemy.orm import Session
from sqlalchemy.pool import StaticPool

from app import auth
from app.database import get_db
from app.models import Base, LoginSession, User


@pytest.fixture
def auth_settings(monkeypatch):
    settings = SimpleNamespace(
        login_username="teacher",
        login_password="test-password-123!",
        cookie_secure=False,
        session_days=7,
    )
    monkeypatch.setattr(auth, "get_settings", lambda: settings)
    auth.login_throttle.clear()
    yield settings
    auth.login_throttle.clear()


@pytest.fixture
def auth_db():
    engine = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine)
    with Session(engine) as db:
        yield db
    engine.dispose()


@pytest.fixture
def auth_client(auth_db, auth_settings):
    application = FastAPI()
    application.include_router(auth.router)
    application.dependency_overrides[get_db] = lambda: auth_db
    with TestClient(application, base_url="https://testserver") as client:
        yield client


def credentials(**changes):
    return {"username": "teacher", "password": "test-password-123!", **changes}


def login(client, **changes):
    return client.post("/auth/login", json=credentials(**changes))


def test_current_profile_requires_a_valid_session(auth_client):
    assert auth_client.get("/auth/me").status_code == 401
    auth_client.cookies.set(auth.SESSION_COOKIE, "forged-session")
    assert auth_client.get("/auth/me").status_code == 401


def test_login_issues_an_opaque_cookie_and_returns_profile(auth_client, auth_db):
    response = login(auth_client)
    assert response.status_code == 200
    assert set(response.json()) == {"id", "username", "display_name"}
    assert response.json()["username"] == "teacher"
    assert response.headers["cache-control"] == "no-store"
    token = auth_client.cookies.get(auth.SESSION_COOKIE)
    stored = auth_db.scalar(select(LoginSession))
    assert token and stored.id == auth.session_digest(token) and stored.id != token
    cookie = response.headers["set-cookie"]
    assert "HttpOnly" in cookie and "SameSite=lax" in cookie
    assert "Max-Age=604800" in cookie
    assert auth_client.get("/auth/me").json() == response.json()
    assert auth_db.scalar(select(User)).google_sub == auth.LOCAL_SUBJECT


def test_production_cookie_is_secure(auth_client, auth_settings):
    auth_settings.cookie_secure = True
    response = login(auth_client)
    assert response.status_code == 200
    assert "Secure" in response.headers["set-cookie"]
    assert auth_client.get("/auth/me").status_code == 200


def test_bad_username_and_password_return_same_error_without_rows(auth_client, auth_db):
    username_error = login(auth_client, username="stranger")
    password_error = login(auth_client, password="wrong-password")
    assert username_error.status_code == password_error.status_code == 401
    assert username_error.json() == password_error.json()
    assert "wrong-password" not in password_error.text
    assert auth_db.scalar(select(User)) is None
    assert auth_db.scalar(select(LoginSession)) is None


@pytest.mark.parametrize("setting", ["login_username", "login_password"])
def test_missing_configuration_fails_closed(auth_client, auth_db, auth_settings, setting):
    setattr(auth_settings, setting, "")
    assert login(auth_client).status_code == 503
    assert auth_db.scalar(select(User)) is None
    assert auth_db.scalar(select(LoginSession)) is None


def test_login_rotates_the_current_session(auth_client, auth_db):
    assert login(auth_client).status_code == 200
    old_token = auth_client.cookies.get(auth.SESSION_COOKIE)
    assert login(auth_client).status_code == 200
    assert auth_client.cookies.get(auth.SESSION_COOKIE) != old_token
    assert auth_db.get(LoginSession, auth.session_digest(old_token)) is None
    assert len(auth_db.scalars(select(LoginSession)).all()) == 1


def test_expired_sessions_are_rejected_and_removed(auth_client, auth_db):
    assert login(auth_client).status_code == 200
    token = auth_client.cookies.get(auth.SESSION_COOKIE)
    stored = auth_db.get(LoginSession, auth.session_digest(token))
    stored.expires_at = datetime.now(UTC) - timedelta(seconds=1)
    auth_db.commit()
    assert auth_client.get("/auth/me").status_code == 401
    assert auth_db.get(LoginSession, auth.session_digest(token)) is None


def test_logout_revokes_server_side_session(auth_client, auth_db):
    assert login(auth_client).status_code == 200
    token = auth_client.cookies.get(auth.SESSION_COOKIE)
    assert auth_client.post("/auth/logout").status_code == 204
    assert auth_db.get(LoginSession, auth.session_digest(token)) is None
    auth_client.cookies.set(auth.SESSION_COOKIE, token)
    assert auth_client.get("/auth/me").status_code == 401


def test_legacy_google_account_cannot_use_the_local_login(auth_client, auth_db):
    user = User(google_sub="old-google-subject", email="teacher@example.com", display_name="Teacher")
    auth_db.add(user)
    auth_db.flush()
    token = "legacy-session"
    auth_db.add(LoginSession(id=auth.session_digest(token), user_id=user.id,
                             expires_at=datetime.now(UTC) + timedelta(days=1)))
    auth_db.commit()
    auth_client.cookies.set(auth.SESSION_COOKIE, token)
    assert auth_client.get("/auth/me").status_code in {401, 403}
    assert auth_client.get("/auth/google").status_code == 404
    assert auth_client.get("/auth/callback").status_code == 404


@pytest.mark.parametrize("setting", ["login_username", "login_password"])
def test_changing_credentials_revokes_existing_sessions(auth_client, auth_settings, setting):
    assert login(auth_client).status_code == 200
    setattr(auth_settings, setting, "changed-credential")
    assert auth_client.get("/auth/me").status_code == 401


def test_changing_username_keeps_the_same_owner(auth_client, auth_db, auth_settings):
    original = login(auth_client).json()
    auth_settings.login_username = "new-teacher"
    renamed = login(auth_client, username="new-teacher")
    assert renamed.status_code == 200
    assert renamed.json()["id"] == original["id"]
    assert renamed.json()["username"] == "new-teacher"
    assert len(auth_db.scalars(select(User)).all()) == 1


def test_unicode_credentials_are_supported(auth_client, auth_settings):
    auth_settings.login_username = "õpetaja"
    auth_settings.login_password = "väga-pikk-parool-123!"
    response = login(auth_client, username=auth_settings.login_username, password=auth_settings.login_password)
    assert response.status_code == 200
    assert auth_client.get("/auth/me").json()["username"] == "õpetaja"


def test_login_throttle_blocks_a_sixth_attempt_then_expires(auth_client, monkeypatch):
    import app.login_throttle as throttle_module

    current = [1000.0]
    monkeypatch.setattr(throttle_module, "monotonic", lambda: current[0])
    for number in range(5):
        assert login(auth_client, username=f"guess-{number}").status_code == 401
    blocked = login(auth_client)
    assert blocked.status_code == 429
    assert int(blocked.headers["retry-after"]) > 0
    current[0] += 61
    assert login(auth_client).status_code == 200


def test_successful_login_resets_failed_attempts(auth_client):
    for _ in range(4):
        assert login(auth_client, password="wrong").status_code == 401
    assert login(auth_client).status_code == 200
    assert login(auth_client, password="wrong").status_code == 401
