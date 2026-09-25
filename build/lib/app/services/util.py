"""Small helpers shared by services."""

import re
import unicodedata

SLUG_MAX = 60


def slug(text: str, fallback: str = "item") -> str:
    """"Mein Board!" -> "mein-board"."""
    norm = unicodedata.normalize("NFKD", text).encode("ascii", "ignore").decode()
    value = re.sub(r"[^a-zA-Z0-9]+", "-", norm).strip("-").lower()
    return value[:SLUG_MAX] or fallback


def unique(base: str, taken: set[str]) -> str:
    if base not in taken:
        return base

    index = 2
    while f"{base}-{index}" in taken:
        index += 1
    return f"{base}-{index}"


class Conflict(Exception):
    """Optimistic lock: somebody saved a newer version meanwhile."""


class NotFound(Exception):
    pass
