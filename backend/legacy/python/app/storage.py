from io import BytesIO
from pathlib import Path
from uuid import uuid4

from fastapi import HTTPException, UploadFile
from PIL import Image, ImageOps, UnidentifiedImageError

from .config import get_settings

MAX_IMAGE_PIXELS = 20_000_000
ALLOWED_FORMATS = {"JPEG", "PNG", "WEBP"}


def normalize_photo(data: bytes) -> bytes:
    try:
        with Image.open(BytesIO(data)) as image:
            if image.format not in ALLOWED_FORMATS or getattr(image, "n_frames", 1) != 1:
                raise HTTPException(415, "Use a single JPEG, PNG, or WebP photo")
            if image.width * image.height > MAX_IMAGE_PIXELS:
                raise HTTPException(413, "Photo exceeds the 20 megapixel limit")
            image.load()
            output = BytesIO()
            ImageOps.exif_transpose(image).convert("RGB").save(output, "JPEG", quality=92)
            return output.getvalue()
    except (UnidentifiedImageError, OSError, ValueError, Image.DecompressionBombError):
        raise HTTPException(415, "The file is not a readable exam photo") from None


def save_photo(upload: UploadFile) -> tuple[str, int]:
    settings = get_settings()
    raw = upload.file.read(settings.max_page_bytes + 1)
    if not raw or len(raw) > settings.max_page_bytes:
        raise HTTPException(413, "Photo is empty or exceeds the upload limit")
    normalized = normalize_photo(raw)
    if len(normalized) > settings.max_page_bytes:
        raise HTTPException(413, "Processed photo exceeds the upload limit")
    settings.upload_dir.mkdir(mode=0o700, parents=True, exist_ok=True)
    key = f"{uuid4()}.jpg"
    path = settings.upload_dir / key
    try:
        with path.open("xb") as output:
            output.write(normalized)
        path.chmod(0o600)
    except OSError:
        path.unlink(missing_ok=True)
        raise
    return key, len(normalized)


def photo_path(storage_key: str) -> Path:
    root = get_settings().upload_dir.resolve()
    path = (root / storage_key).resolve()
    if path.parent != root:
        raise HTTPException(404, "Photo not found")
    return path


def remove_photos(keys: list[str]) -> None:
    for key in keys:
        photo_path(key).unlink(missing_ok=True)
