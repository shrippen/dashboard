"""Widget type contract and registry.

A widget type declares what it needs, never how to get it:

    WidgetType(key="rss", schema=RssConfig, template="widgets/rss.html",
               queries=lambda cfg: [Query("feed", "rss", {"url": cfg.url})])

The widget service runs the queries (with access checks and caching) and
hands the results to the template.
"""

from collections.abc import Callable
from dataclasses import dataclass, field
from enum import StrEnum

from pydantic import BaseModel

from app.enums import ServiceType


class ConnUse(StrEnum):
    NONE = "none"
    WIDGET = "widget"
    INFO = "info"


class Extra(StrEnum):
    """Additional data the widget service supplies besides the queries."""

    NONE = "none"
    HINTS = "hints"
    POINTS = "points"


@dataclass(frozen=True)
class ViewCtx:
    """What a view function may use: no I/O, only values."""

    today: object
    settings: dict
    options: dict
    service: str | None


class Category(StrEnum):
    START = "start"
    INSIGHT = "insight"


@dataclass(frozen=True)
class Query:
    name: str
    source: str
    params: dict = field(default_factory=dict)
    conn: ConnUse = ConnUse.NONE


def no_queries(_config: BaseModel) -> list[Query]:
    return []


@dataclass(frozen=True)
class WidgetType:
    key: str
    schema: type[BaseModel]
    template: str
    category: Category = Category.START
    service: ServiceType | None = None
    refresh_s: int | None = None
    # Inline widgets render with the page (search needs link tiles in the HTML).
    inline: bool = False
    queries: Callable[[BaseModel], list[Query]] = no_queries
    # Shapes query results for the template: view(config, {slot: data}, ViewCtx) -> dict
    view: Callable[[BaseModel, dict, ViewCtx], dict] | None = None
    extra: Extra = Extra.NONE


_registry: dict[str, WidgetType] = {}


def register(kind: WidgetType) -> WidgetType:
    _registry[kind.key] = kind
    return kind


def get(key: str) -> WidgetType | None:
    return _registry.get(key)


def all_types() -> list[WidgetType]:
    return sorted(_registry.values(), key=lambda k: (k.category, k.key))


def parse(key: str, config: dict) -> BaseModel:
    kind = get(key)
    if kind is None:
        raise KeyError(key)

    return kind.schema.model_validate(config or {})
