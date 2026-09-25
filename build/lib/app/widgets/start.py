"""Start page widgets (Dashy replacements)."""

from enum import StrEnum

from pydantic import BaseModel, Field, field_validator

from app.enums import LinkTarget, ServiceType
from app.widgets.base import ConnUse, Query, WidgetType, register

WEB_SCHEMES = ("http://", "https://")
DEFAULT_TIMEZONE = "Europe/Berlin"


class StatusMode(StrEnum):
    OFF = "off"
    HTTP = "http"


def _web_url(value: str) -> str:
    value = (value or "").strip()
    if value and not value.startswith(WEB_SCHEMES):
        raise ValueError("url.scheme")
    return value


class InfoRef(BaseModel):
    connection: str
    metric: str = "summary"


class LinkConfig(BaseModel):
    url: str
    description: str = ""
    icon: str = ""
    target: LinkTarget = LinkTarget.NEW_TAB
    status: StatusMode = StatusMode.HTTP
    status_url: str = ""
    accept: list[int] = Field(default_factory=list)
    insecure: bool = False
    hotkey: str = ""
    info: InfoRef | None = None

    _check_url = field_validator("url", "status_url")(_web_url)


def _link_queries(cfg: LinkConfig) -> list[Query]:
    found = []
    if cfg.status == StatusMode.HTTP:
        params = {"url": cfg.status_url or cfg.url, "accept": cfg.accept, "insecure": cfg.insecure}
        found.append(Query("status", "http_status", params))
    if cfg.info:
        found.append(Query("info", "info", {"metric": cfg.info.metric}, ConnUse.INFO))
    return found


class RssConfig(BaseModel):
    url: str
    limit: int = Field(default=8, ge=1, le=50)
    summary: bool = False

    _check_url = field_validator("url")(_web_url)


class ClockConfig(BaseModel):
    timezones: list[str] = Field(default_factory=lambda: [DEFAULT_TIMEZONE])
    seconds: bool = False
    date: bool = True


class WeatherConfig(BaseModel):
    label: str = ""
    lat: float = Field(ge=-90, le=90)
    lon: float = Field(ge=-180, le=180)


class IframeConfig(BaseModel):
    url: str
    height: int = Field(default=320, ge=80, le=2000)

    _check_url = field_validator("url")(_web_url)


class EmptyConfig(BaseModel):
    pass


class NoteConfig(BaseModel):
    text: str = ""


register(WidgetType("link", LinkConfig, "widgets/link.html", inline=True, queries=_link_queries))
register(
    WidgetType(
        "rss",
        RssConfig,
        "widgets/rss.html",
        refresh_s=30 * 60,
        queries=lambda c: [Query("feed", "rss", {"url": c.url, "limit": c.limit})],
    )
)
register(WidgetType("clock", ClockConfig, "widgets/clock.html", inline=True))
register(
    WidgetType(
        "weather",
        WeatherConfig,
        "widgets/weather.html",
        refresh_s=30 * 60,
        queries=lambda c: [Query("weather", "open_meteo", {"lat": c.lat, "lon": c.lon})],
    )
)
register(WidgetType("iframe", IframeConfig, "widgets/iframe.html", inline=True))
register(
    WidgetType(
        "sysinfo",
        EmptyConfig,
        "widgets/sysinfo.html",
        service=ServiceType.GLANCES,
        refresh_s=60,
        queries=lambda c: [Query("stats", "glances", {}, ConnUse.WIDGET)],
    )
)
register(
    WidgetType(
        "public_ip",
        EmptyConfig,
        "widgets/public_ip.html",
        refresh_s=60 * 60,
        queries=lambda c: [Query("ip", "public_ip", {})],
    )
)
register(WidgetType("note", NoteConfig, "widgets/note.html", inline=True))
