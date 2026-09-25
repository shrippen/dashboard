"""Icon download adapter: resolves an icon spec to image bytes.

    si-github        → Simple Icons (SVG)
    hl-kimai         → Dashboard Icons (SVG, PNG fallback)
    favicon + url    → <origin>/favicon.ico
    https://…/x.png  → as is
"""

import re
from dataclasses import dataclass
from urllib.parse import urlparse

from app.drivers import http
from app.sources.base import SourceError

SIMPLE_ICONS = "https://cdn.jsdelivr.net/npm/simple-icons@latest/icons/{name}.svg"
DASHBOARD_ICONS = "https://cdn.jsdelivr.net/gh/homarr-labs/dashboard-icons/{ext}/{name}.{ext}"
MAX_ICON = 512 * 1024
OK = 200
IMAGE_TYPES = ("image/svg+xml", "image/png", "image/x-icon", "image/vnd.microsoft.icon",
               "image/jpeg", "image/webp", "image/gif")

_SCRIPT = re.compile(r"<script.*?</script>", re.I | re.S)
_HANDLER = re.compile(r"\son[a-z]+\s*=\s*(\"[^\"]*\"|'[^']*')", re.I)
_EXTERNAL = re.compile(r"(xlink:)?href\s*=\s*(\"(?!#)[^\"]*\"|'(?!#)[^']*')", re.I)


@dataclass(frozen=True)
class Icon:
    body: bytes
    media_type: str


def candidates(spec: str, page_url: str) -> list[str]:
    if spec.startswith("si-"):
        return [SIMPLE_ICONS.format(name=spec[3:])]
    if spec.startswith("hl-"):
        name = spec[3:]
        return [DASHBOARD_ICONS.format(ext=ext, name=name) for ext in ("svg", "png")]
    if spec == "favicon":
        origin = urlparse(page_url)
        return [f"{origin.scheme}://{origin.netloc}/favicon.ico"] if origin.netloc else []
    if spec.startswith(("http://", "https://")):
        return [spec]
    return []


def clean_svg(body: bytes) -> bytes:
    """Remove scripts, event handlers and external references."""
    text = body.decode("utf-8", "ignore")
    text = _SCRIPT.sub("", text)
    text = _HANDLER.sub("", text)
    text = _EXTERNAL.sub("", text)
    return text.encode()


def fetch(spec: str, page_url: str) -> Icon:
    for url in candidates(spec, page_url):
        try:
            response = http.request("GET", url, timeout=10)
        except (http.HttpError, http.EgressDenied):
            continue
        if response.status_code != OK or len(response.content) > MAX_ICON:
            continue

        media = response.headers.get("content-type", "").split(";")[0].strip().lower()
        if url.endswith(".svg") or media == "image/svg+xml":
            return Icon(clean_svg(response.content), "image/svg+xml")
        if media in IMAGE_TYPES or url.endswith(".ico"):
            return Icon(response.content, media or "image/x-icon")

    raise SourceError("icon not found")
