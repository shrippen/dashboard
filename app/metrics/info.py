"""Info line of a link tile: two or three short facts per service.

    parts = [{"key": "kimai.today", "params": {"hours": "3,5"}}]   (key → catalog "info.<key>")
"""

from datetime import UTC, date, datetime

from app.metrics import kimai as km
from app.metrics import ninja as nm
from app.metrics import snipe as sm
from app.metrics.dates import parse_time

MINUTES_PER_HOUR = 60


def _part(key: str, **params) -> dict:
    return {"key": key, "params": params}


def parts(service: str, data: dict, today: date) -> list[dict]:
    builder = {"kimai": _kimai, "invoiceninja": _ninja, "snipeit": _snipe, "dawarich": _dawarich,
               "glances": _glances}.get(service)
    return builder(data, today) if builder else []


def _kimai(data: dict, today: date) -> list[dict]:
    stats = km.summary(data, today)
    found = [_part("kimai.today", hours={"$num": stats["today_min"] / MINUTES_PER_HOUR, "digits": 1})]
    if stats["running"]:
        found.append(_part("kimai.running"))
    return found


def _ninja(data: dict, today: date) -> list[dict]:
    stats = nm.summary(data, today)
    found = []
    if stats["overdue"]:
        found.append(_part("in.overdue", count=len(stats["overdue"])))
    found.append(_part("in.open", amount={"$money": stats["open_amount"], "currency": stats["currency"]}))
    return found


def _snipe(data: dict, today: date) -> list[dict]:
    stats = sm.summary(data, today)
    return [_part("snipe.assets", count=stats["assets"]),
            _part("snipe.upcoming", count=len(stats["upcoming"]))]


def _dawarich(data: dict, today: date) -> list[dict]:
    last = parse_time(data.get("last_point"))
    if last is None:
        return []
    if last.tzinfo is None:
        last = last.replace(tzinfo=UTC)
    hours = int((datetime.now(UTC) - last).total_seconds() // 3600)
    return [_part("dawarich.last", hours=hours)]


def _glances(data: dict, today: date) -> list[dict]:
    return [_part("glances.cpu", percent=round(data.get("cpu") or 0))]
