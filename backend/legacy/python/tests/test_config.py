import pytest
from pydantic import ValidationError

from app.config import Settings


@pytest.mark.parametrize("origin", [
    "https://example.com?bad=1", "https://example.com#", "https://user:password@example.com",
    "https://example.com:invalid", "https://example.com:70000", "https://example.com/app",
    "https://bad host", "*",
])
def test_invalid_origins_are_rejected(origin):
    with pytest.raises(ValidationError):
        Settings(public_base_url=origin)
    with pytest.raises(ValidationError):
        Settings(frontend_origins=[origin])


def test_production_requires_https_and_secure_cookies():
    with pytest.raises(ValidationError):
        Settings(app_env="production", public_base_url="http://example.com", cookie_secure=True)
    with pytest.raises(ValidationError):
        Settings(app_env="production", public_base_url="https://example.com", cookie_secure=False)
    settings = Settings(app_env="production", public_base_url="https://example.com/", cookie_secure=True)
    assert settings.public_base_url == "https://example.com"
