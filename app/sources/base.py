"""Source contract and registry.

A source turns one query against one service into JSON-able domain data:

    Source("rss").fetch(Ctx(params={"url": ..., "limit": 8}))
        -> {"items": [{"title": ..., "link": ..., "published": ...}]}

Sources never see users or permissions; services decide who may ask.
"""

from dataclasses import dataclass, field
from datetime import timedelta
from typing import Protocol

from app.enums import ServiceType


class SourceError(Exception):
    """Expected failure (service down, bad token); message is shown to the user."""


@dataclass(frozen=True)
class Ctx:
    url: str = ""
    secret: str | None = None
    verify_tls: bool = True
    options: dict = field(default_factory=dict)
    params: dict = field(default_factory=dict)


class Source(Protocol):
    key: str
    ttl: timedelta
    service: ServiceType | None

    def fetch(self, ctx: Ctx) -> dict: ...


_registry: dict[str, Source] = {}


def register(source: Source) -> Source:
    _registry[source.key] = source
    return source


def get(key: str) -> Source:
    if key not in _registry:
        raise KeyError(f"unknown source {key}")

    return _registry[key]


def all_sources() -> dict[str, Source]:
    return dict(_registry)
