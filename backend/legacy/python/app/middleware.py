from starlette.datastructures import Headers
from starlette.exceptions import HTTPException
from starlette.responses import JSONResponse

from .config import get_settings


class RequestGuard:
    """Limit uploads and reject cross-origin cookie-authenticated mutations."""

    def __init__(self, app):
        self.app = app

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        headers, settings = Headers(scope=scope), get_settings()
        if scope["method"] not in {"GET", "HEAD", "OPTIONS"}:
            if headers.get("origin") not in settings.allowed_origins:
                return await JSONResponse({"detail": "Origin not allowed"}, 403)(scope, receive, send)
        multipart = headers.get("content-type", "").startswith("multipart/form-data")
        limit = settings.max_request_bytes if multipart else 65536
        try:
            length = int(headers.get("content-length", "0"))
        except ValueError:
            return await JSONResponse({"detail": "Invalid Content-Length"}, 400)(scope, receive, send)
        if length < 0 or length > limit:
            return await JSONResponse({"detail": "Request exceeds the size limit"}, 413)(scope, receive, send)
        received = 0

        async def bounded_receive():
            nonlocal received
            message = await receive()
            received += len(message.get("body", b""))
            if received > limit:
                raise HTTPException(413, "Request exceeds the size limit")
            return message

        await self.app(scope, bounded_receive, send)
