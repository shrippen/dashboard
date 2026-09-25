"""Generic web sources: status checks, feeds, weather, system stats, public IP."""

import time
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from email.utils import parsedate_to_datetime

import feedparser
import nh3

from app.drivers import http
from app.enums import ServiceType
from app.sources.base import Ctx, SourceError, register

HTTP_OK_MIN = 200
HTTP_OK_MAX = 399
SUMMARY_LEN = 240
FEED_LIMIT_MAX = 50
OPEN_METEO_URL = "https://api.open-meteo.com/v1/forecast"
PUBLIC_IP_URL = "https://api.ipify.org"
GLANCES_API = 4
FORECAST_DAYS = 4


@dataclass(frozen=True)
class HttpStatus:
    key: str = "http_status"
    ttl: timedelta = timedelta(minutes=5)
    service: ServiceType | None = None

    def fetch(self, ctx: Ctx) -> dict:
        """Up/down with response time. A failed check is data, not an error."""
        url = ctx.params["url"]
        accept = set(ctx.params.get("accept") or [])
        started = time.monotonic()
        try:
            response = http.request(
                "GET", url, verify=not ctx.params.get("insecure"), timeout=10, follow=True
            )
        except http.EgressDenied:
            return {"up": False, "code": None, "ms": None, "error": "egress"}
        except http.HttpError as exc:
            return {"up": False, "code": None, "ms": None, "error": str(exc)}

        ms = round((time.monotonic() - started) * 1000)
        code = response.status_code
        up = code in accept if accept else HTTP_OK_MIN <= code <= HTTP_OK_MAX
        return {"up": up, "code": code, "ms": ms, "error": None}


def _published(entry) -> str | None:
    for field in ("published", "updated"):
        raw = entry.get(field)
        if not raw:
            continue
        try:
            return parsedate_to_datetime(raw).astimezone(UTC).isoformat()
        except (TypeError, ValueError):
            parsed = entry.get(f"{field}_parsed")
            if parsed:
                return datetime(*parsed[:6], tzinfo=UTC).isoformat()

    return None


def _plain(html: str) -> str:
    """Feed HTML → plain text: no markup from third parties reaches the page."""
    text = nh3.clean(html or "", tags=set())
    text = " ".join(text.split())
    return text[:SUMMARY_LEN] + ("…" if len(text) > SUMMARY_LEN else "")


@dataclass(frozen=True)
class Feed:
    key: str = "rss"
    ttl: timedelta = timedelta(minutes=30)
    service: ServiceType | None = None

    def fetch(self, ctx: Ctx) -> dict:
        try:
            response = http.request("GET", ctx.params["url"])
        except (http.HttpError, http.EgressDenied) as exc:
            raise SourceError(str(exc)) from exc

        parsed = feedparser.parse(response.content)
        if parsed.bozo and not parsed.entries:
            raise SourceError("invalid feed")

        limit = min(int(ctx.params.get("limit") or 8), FEED_LIMIT_MAX)
        items = []
        for entry in parsed.entries[:limit]:
            link = entry.get("link", "")
            if not link.startswith(("http://", "https://")):
                link = ""
            items.append(
                {
                    "title": _plain(entry.get("title", ""))[:200],
                    "link": link,
                    "published": _published(entry),
                    "summary": _plain(entry.get("summary", "")),
                }
            )

        return {"title": _plain(parsed.feed.get("title", "")), "items": items}


@dataclass(frozen=True)
class Weather:
    key: str = "open_meteo"
    ttl: timedelta = timedelta(minutes=30)
    service: ServiceType | None = None

    def fetch(self, ctx: Ctx) -> dict:
        params = {
            "latitude": ctx.params["lat"],
            "longitude": ctx.params["lon"],
            "current": "temperature_2m,weather_code,wind_speed_10m,is_day",
            "daily": "weather_code,temperature_2m_max,temperature_2m_min",
            "timezone": "auto",
            "forecast_days": FORECAST_DAYS,
        }
        try:
            raw = http.get_json(OPEN_METEO_URL, params=params)
        except (http.HttpError, http.EgressDenied) as exc:
            raise SourceError(str(exc)) from exc

        current = raw.get("current", {})
        daily = raw.get("daily", {})
        days = [
            {"day": d, "code": c, "max": hi, "min": lo}
            for d, c, hi, lo in zip(
                daily.get("time", []),
                daily.get("weather_code", []),
                daily.get("temperature_2m_max", []),
                daily.get("temperature_2m_min", []),
                strict=False,
            )
        ]
        return {
            "temp": current.get("temperature_2m"),
            "code": current.get("weather_code"),
            "wind": current.get("wind_speed_10m"),
            "is_day": bool(current.get("is_day", 1)),
            "days": days,
        }


@dataclass(frozen=True)
class Glances:
    key: str = "glances"
    ttl: timedelta = timedelta(minutes=1)
    service: ServiceType | None = ServiceType.GLANCES

    def fetch(self, ctx: Ctx) -> dict:
        version = ctx.options.get("api_version", GLANCES_API)
        base = f"{ctx.url.rstrip('/')}/api/{version}"
        headers = {"Authorization": f"Bearer {ctx.secret}"} if ctx.secret else None
        try:
            quick = http.get_json(f"{base}/quicklook", headers=headers, verify=ctx.verify_tls)
            disks = http.get_json(f"{base}/fs", headers=headers, verify=ctx.verify_tls)
            load = http.get_json(f"{base}/load", headers=headers, verify=ctx.verify_tls)
        except (http.HttpError, http.EgressDenied) as exc:
            raise SourceError(str(exc)) from exc

        return {
            "cpu": quick.get("cpu"),
            "mem": quick.get("mem"),
            "swap": quick.get("swap"),
            "load": load.get("min5"),
            "disks": [
                {"mount": d.get("mnt_point"), "percent": d.get("percent")}
                for d in (disks if isinstance(disks, list) else [])
            ],
        }


@dataclass(frozen=True)
class PublicIp:
    key: str = "public_ip"
    ttl: timedelta = timedelta(hours=1)
    service: ServiceType | None = None

    def fetch(self, ctx: Ctx) -> dict:
        try:
            raw = http.get_json(PUBLIC_IP_URL, params={"format": "json"})
        except (http.HttpError, http.EgressDenied) as exc:
            raise SourceError(str(exc)) from exc

        return {"ip": raw.get("ip")}


for _source in (HttpStatus(), Feed(), Weather(), Glances(), PublicIp()):
    register(_source)
