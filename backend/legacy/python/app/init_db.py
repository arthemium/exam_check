"""Create the initial schema on a fresh database; not a migration runner."""

from . import models  # noqa: F401
from .database import Base, engine


def main():
    Base.metadata.create_all(engine)


if __name__ == "__main__":
    main()
