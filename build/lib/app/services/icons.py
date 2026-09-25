"""Icon cache: download once, serve locally, never block a page render.

    url_for(spec) ──cached?──► /icons/<hash>
                  └─missing──► None (template shows a monogram), fetch in background
"""

import hashlib
import json
import logging
import threading
from pathlib import Path

from app.settings import get_settings
from app.sources import icons as source
from app.sources.base import SourceError

log = logging.getLogger(__name__)

MISS_SUFFIX = ".miss"
META_SUFFIX = ".json"
UPLOAD_PREFIX = "upload:"
MAX_UPLOAD = 512 * 1024
UPLOAD_TYPES = {"image/svg+xml", "image/png", "image/webp", "image/jpeg", "image/x-icon"}

_pending: set[str] = set()
_pending_lock = threading.Lock()


class IconError(ValueError):
    pass


def _key(spec: str, page_url: str) -> str:
    raw = spec if spec != "favicon" else f"favicon:{page_url}"
    return hashlib.sha256(raw.encode()).hexdigest()[:24]


def _dir() -> Path:
    path = get_settings().icons_dir
    path.mkdir(parents=True, exist_ok=True)
    return path


def url_for(spec: str, page_url: str = "") -> str | None:
    spec = (spec or "").strip()
    if not spec:
        return None
    if spec.startswith(UPLOAD_PREFIX):
        return f"/icons/{spec[len(UPLOAD_PREFIX):]}"

    key = _key(spec, page_url)
    folder = _dir()
    if (folder / key).exists():
        return f"/icons/{key}"
    if (folder / (key + MISS_SUFFIX)).exists():
        return None

    _schedule(spec, page_url, key)
    return None


def _schedule(spec: str, page_url: str, key: str) -> None:
    with _pending_lock:
        if key in _pending:
            return
        _pending.add(key)

    threading.Thread(target=_download, args=(spec, page_url, key), daemon=True).start()


def _download(spec: str, page_url: str, key: str) -> None:
    folder = _dir()
    try:
        icon = source.fetch(spec, page_url)
        (folder / key).write_bytes(icon.body)
        (folder / (key + META_SUFFIX)).write_text(json.dumps({"type": icon.media_type}))
    except SourceError:
        (folder / (key + MISS_SUFFIX)).write_text(spec)
    except Exception:  # Icons are cosmetic; failures only get logged.
        log.exception("icon %s failed", spec)
    finally:
        with _pending_lock:
            _pending.discard(key)


def read(key: str) -> tuple[bytes, str] | None:
    if not key.isalnum():
        return None

    folder = _dir()
    path = folder / key
    if not path.is_file():
        return None

    meta = folder / (key + META_SUFFIX)
    media = json.loads(meta.read_text()).get("type") if meta.exists() else "image/png"
    return path.read_bytes(), media


def upload(body: bytes, media_type: str) -> str:
    """Store an uploaded icon; returns the spec to put into a link widget."""
    if len(body) > MAX_UPLOAD or media_type not in UPLOAD_TYPES:
        raise IconError("icon.invalid")

    if media_type == "image/svg+xml":
        body = source.clean_svg(body)
    key = hashlib.sha256(body).hexdigest()[:24]
    folder = _dir()
    (folder / key).write_bytes(body)
    (folder / (key + META_SUFFIX)).write_text(json.dumps({"type": media_type}))
    return UPLOAD_PREFIX + key


def forget_misses() -> None:
    """Retry failed icons (daily)."""
    for path in _dir().glob("*" + MISS_SUFFIX):
        path.unlink(missing_ok=True)
