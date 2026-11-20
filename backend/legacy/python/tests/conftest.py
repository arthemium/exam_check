import os

os.environ.setdefault("APP_ENV", "test")
os.environ.setdefault("DATABASE_URL", "sqlite://")
os.environ.setdefault("LOGIN_USERNAME", "teacher")
os.environ.setdefault("LOGIN_PASSWORD", "test-password-never-used-outside-tests")
