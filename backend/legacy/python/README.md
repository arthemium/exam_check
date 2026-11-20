# Previous Python implementation

The former FastAPI/PostgreSQL source and tests are retained here for reference.
The current application is the Go implementation in `backend/cmd` and
`backend/internal`; Docker excludes this directory.

These files do not import PostgreSQL data into SQLite. The current Compose setup
does not run the Python application.
