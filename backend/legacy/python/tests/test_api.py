from io import BytesIO
from types import SimpleNamespace

import pytest
from fastapi.testclient import TestClient
from PIL import Image
from sqlalchemy import create_engine, event, func, select
from sqlalchemy.orm import sessionmaker
from sqlalchemy.pool import StaticPool

from app.auth import get_current_user
from app.config import get_settings
from app.database import Base, get_db
from app.main import create_app
from app.models import CheckJob, Exam, User


@pytest.fixture
def api(monkeypatch, tmp_path):
    monkeypatch.setenv("UPLOAD_DIR", str(tmp_path / "uploads"))
    monkeypatch.setenv("OPENAI_API_KEY", "test-key-never-sent")
    monkeypatch.setenv("OPENAI_MODEL", "test-model")
    get_settings.cache_clear()
    engine = create_engine("sqlite://", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    event.listen(engine, "connect", lambda conn, _: conn.execute("PRAGMA foreign_keys=ON"))
    Base.metadata.create_all(engine)
    sessions = sessionmaker(engine, expire_on_commit=False)
    users = [User(id=name, google_sub=name, email=f"{name}@example.com", display_name=name)
             for name in ("alice", "bob")]
    with sessions.begin() as db:
        db.add_all(users)
    state = SimpleNamespace(user=users[0], sessions=sessions, settings=get_settings(), users=users)

    def database():
        with sessions() as db:
            yield db

    application = create_app()
    application.dependency_overrides[get_db] = database
    application.dependency_overrides[get_current_user] = lambda: state.user
    with TestClient(application, headers={"Origin": "http://localhost:8000"}) as client:
        state.client, state.app = client, application
        yield state
    engine.dispose()
    get_settings.cache_clear()


def photo_bytes() -> bytes:
    output = BytesIO()
    Image.new("RGB", (40, 30), color="white").save(output, "PNG")
    return output.getvalue()


def folder(api) -> dict:
    response = api.client.post("/api/folders", json={"name": "Class A"})
    assert response.status_code == 201
    return response.json()


def upload(api, folder_id, *, photos=None, checks=None):
    return api.client.post(
        f"/api/folders/{folder_id}/exams",
        data={"title": " Essay 1 ", "student_name": " Alice Smith ",
              "checks": ["grammar", "spelling"] if checks is None else checks},
        files=photos or [("photos", ("essay.png", photo_bytes(), "image/png"))],
    )


def test_folder_names_counts_and_user_isolation(api):
    item = folder(api)
    assert api.client.post("/api/folders", json={"name": " class a "}).status_code == 409
    assert api.client.post("/api/folders", json={"name": "   "}).status_code == 422
    assert upload(api, item["id"]).status_code == 201
    assert api.client.get("/api/folders").json()[0]["exam_count"] == 1
    assert api.client.patch(f"/api/folders/{item['id']}", json={"name": "Class B"}).json()["name"] == "Class B"
    api.user = api.users[1]
    assert api.client.get("/api/folders").json() == []
    assert api.client.get(f"/api/folders/{item['id']}/exams").status_code == 404
    assert api.client.patch(f"/api/folders/{item['id']}", json={"name": "Stolen"}).status_code == 404
    assert upload(api, item["id"]).status_code == 404


def test_upload_pages_are_ordered_private_and_normalized(api):
    item = folder(api)
    files = [("photos", (name, photo_bytes(), "application/octet-stream")) for name in ("../one.png", "two.png")]
    response = upload(api, item["id"], photos=files)
    assert response.status_code == 201
    exam = response.json()
    assert exam["title"] == "Essay 1" and exam["student_name"] == "Alice Smith"
    assert [page["page_number"] for page in exam["pages"]] == [1, 2]
    assert len(api.client.get(f"/api/folders/{item['id']}/exams").json()) == 1
    page = api.client.get(exam["pages"][0]["url"])
    assert page.status_code == 200 and page.headers["content-type"] == "image/jpeg"
    assert page.headers["cache-control"] == "private, no-store"
    with Image.open(BytesIO(page.content)) as image:
        assert image.format == "JPEG" and not image.getexif()
    api.user = api.users[1]
    assert api.client.get(f"/api/exams/{exam['id']}").status_code == 404
    assert api.client.get(exam["pages"][0]["url"]).status_code == 404
    assert api.client.patch(f"/api/exams/{exam['id']}", json={"title": "Stolen"}).status_code == 404
    assert api.client.post(f"/api/exams/{exam['id']}/checks").status_code == 404


def test_invalid_second_photo_rolls_back_files_and_database(api):
    item = folder(api)
    files = [("photos", ("one.png", photo_bytes(), "image/png")),
             ("photos", ("fake.jpg", b"not an image", "image/jpeg"))]
    assert upload(api, item["id"], photos=files).status_code == 415
    assert list(api.settings.upload_dir.iterdir()) == []
    assert api.client.get(f"/api/folders/{item['id']}/exams").json() == []


def test_total_photo_limit_cleans_up(api):
    item = folder(api)
    api.settings.max_exam_bytes = 1
    assert upload(api, item["id"]).status_code == 413
    assert list(api.settings.upload_dir.iterdir()) == []
    assert api.client.get(f"/api/folders/{item['id']}/exams").json() == []


def test_rename_preserves_result_and_criteria_change_resets_it(api):
    exam = upload(api, folder(api)["id"]).json()
    with api.sessions.begin() as db:
        saved = db.get(Exam, exam["id"])
        saved.status, saved.result = "completed", {"transcript": "original"}
    url = f"/api/exams/{exam['id']}"
    assert api.client.patch(url, json={"title": "New title", "student_name": "Jane"}).status_code == 200
    assert api.client.get(url).json()["result"] == {"transcript": "original"}
    assert api.client.patch(url, json={"checks": ["structure"]}).status_code == 200
    updated = api.client.get(url).json()
    assert updated["result"] is None and updated["status"] == "uploaded"
    assert api.client.patch(url, json={"title": None}).status_code == 422


def test_queue_is_idempotent_and_blocks_criteria_changes(api):
    exam = upload(api, folder(api)["id"]).json()
    url = f"/api/exams/{exam['id']}"
    first = api.client.post(f"{url}/checks")
    assert first.status_code == 202
    assert api.client.post(f"{url}/checks").json()["id"] == first.json()["id"]
    assert api.client.get(url).json()["status"] == "queued"
    assert api.client.patch(url, json={"checks": ["structure"]}).status_code == 409
    assert api.client.patch(url, json={"student_name": "Jane"}).status_code == 200
    with api.sessions() as db:
        assert db.scalar(select(func.count()).select_from(CheckJob)) == 1


def test_queue_requires_configuration_and_checks(api):
    exam = upload(api, folder(api)["id"], checks=[]).json()
    url = f"/api/exams/{exam['id']}/checks"
    assert api.client.post(url).status_code == 422
    api.settings.openai_api_key = ""
    assert api.client.post(url).status_code == 503


@pytest.mark.parametrize("limit", ["max_active_jobs_per_user", "max_jobs_per_user_per_day"])
def test_queue_enforces_per_user_limits(api, limit):
    item = folder(api)
    setattr(api.settings, limit, 1)
    exams = [upload(api, item["id"]).json() for _ in range(2)]
    assert api.client.post(f"/api/exams/{exams[0]['id']}/checks").status_code == 202
    assert api.client.post(f"/api/exams/{exams[1]['id']}/checks").status_code == 429


def test_unauthenticated_access_and_csrf_rejection(api):
    api.app.dependency_overrides.pop(get_current_user)
    assert api.client.get("/api/folders").status_code == 401
    assert api.client.post("/api/folders", json={"name": "A"}, headers={"Origin": "https://evil.example"}).status_code == 403
    api.client.headers.pop("Origin")
    assert api.client.post("/auth/logout").status_code == 403


def test_request_size_limit_with_and_without_content_length(api):
    oversized = b" " * 65537
    assert api.client.post("/api/folders", content=oversized, headers={"Content-Type": "application/json"}).status_code == 413
    assert api.client.post("/api/folders", content=iter([oversized]), headers={"Content-Type": "application/json"}).status_code == 413
    assert api.client.get("/health/ready").status_code == 200


def test_login_cookie_authorizes_folder_and_photo_access(api):
    api.app.dependency_overrides.pop(get_current_user)
    credentials = {"username": api.settings.login_username, "password": api.settings.login_password}
    assert api.client.post("/auth/login", json=credentials, headers={"Origin": "https://evil.example"}).status_code == 403
    assert api.client.post("/auth/login", json=credentials).status_code == 200
    exam = upload(api, folder(api)["id"]).json()
    assert api.client.get(exam["pages"][0]["url"]).status_code == 200
    assert api.client.post("/auth/logout").status_code == 204
    assert api.client.get(exam["pages"][0]["url"]).status_code == 401


def test_malformed_login_does_not_echo_password(api):
    response = api.client.post("/auth/login", json={"password": "private-test-password"})
    assert response.status_code == 422
    assert "private-test-password" not in response.text
