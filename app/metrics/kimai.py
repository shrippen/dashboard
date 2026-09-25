"""Kimai metrics: hours, utilisation, unbilled work, budgets.

    data = sources.services.KimaiData dataset
    summary(data, today, cfg) → {"today_min": 210, "month_min": 6300, "unbilled": [...], ...}
"""

from collections import defaultdict
from datetime import UTC, date, datetime, timedelta
from enum import StrEnum

from app.metrics.dates import expand, month_start, parse_day, parse_time, week_start, workdays

MINUTES_PER_HOUR = 60
DEFAULT_HOURS_PER_DAY = 8


class Hours(StrEnum):
    ALL = "all"
    BILLABLE = "billable"


def _day(sheet: dict) -> date | None:
    return parse_day(sheet.get("begin"))


def free_days(data: dict) -> set[date]:
    """Public holidays and approved absences (kimai-holiday-bundle)."""
    days: set[date] = set()
    for holiday in data.get("holidays", []):
        if not holiday.get("half_day") and holiday.get("date"):
            days.add(parse_day(holiday["date"]))
    for absence in data.get("absences", []):
        if absence.get("status") in (None, "approved") and not absence.get("half_day"):
            days |= expand(absence.get("start"), absence.get("end"))
    return days


def minutes_between(data: dict, start: date, end: date, kind: Hours = Hours.ALL) -> int:
    total = 0
    for sheet in data.get("timesheets", []):
        day = _day(sheet)
        if day is None or not (start <= day <= end):
            continue
        if kind == Hours.BILLABLE and not sheet.get("billable"):
            continue
        total += sheet.get("minutes", 0)
    return total


def value_between(data: dict, start: date, end: date) -> float:
    return sum(s.get("rate", 0) for s in data.get("timesheets", [])
               if (d := _day(s)) and start <= d <= end and s.get("billable"))


def running(data: dict, now: datetime) -> list[dict]:
    result = []
    for sheet in data.get("active", []):
        begin = parse_time(sheet.get("begin"))
        if begin is None:
            continue
        if begin.tzinfo is None:
            begin = begin.replace(tzinfo=UTC)
        result.append({**sheet, "running_min": int((now - begin).total_seconds() // 60)})
    return result


def customers(data: dict) -> dict:
    return {c["id"]: c["name"] for c in data.get("customers", [])}


def unbilled(data: dict, today: date) -> list[dict]:
    """Billable, finished, not exported entries per customer (like the abrechnung bundle)."""
    groups: dict = defaultdict(lambda: {"minutes": 0, "amount": 0.0, "oldest": None})
    for sheet in data.get("timesheets", []):
        if not sheet.get("billable") or sheet.get("exported") or not sheet.get("end"):
            continue
        day = _day(sheet)
        group = groups[sheet.get("customer_id")]
        group["minutes"] += sheet.get("minutes", 0)
        group["amount"] += sheet.get("rate", 0)
        if day and (group["oldest"] is None or day < group["oldest"]):
            group["oldest"] = day

    names = customers(data)
    return sorted(
        [{"customer_id": cid, "customer": names.get(cid, "?"), "minutes": g["minutes"],
          "amount": round(g["amount"], 2), "oldest": g["oldest"].isoformat() if g["oldest"] else None,
          "age": (today - g["oldest"]).days if g["oldest"] else 0}
         for cid, g in groups.items()],
        key=lambda x: -x["age"],
    )


def target_minutes(data: dict, start: date, end: date, hours_per_day: float) -> int:
    return round(len(workdays(start, end, free_days(data))) * hours_per_day * MINUTES_PER_HOUR)


def summary(data: dict, today: date, hours_per_day: float = DEFAULT_HOURS_PER_DAY) -> dict:
    now = datetime.now(UTC)
    month = month_start(today)
    month_min = minutes_between(data, month, today)
    billable_month = minutes_between(data, month, today, kind=Hours.BILLABLE)
    target = target_minutes(data, month, today, hours_per_day)
    return {
        "today_min": minutes_between(data, today, today),
        "week_min": minutes_between(data, week_start(today), today),
        "month_min": month_min,
        "billable_month_min": billable_month,
        "target_month_min": target,
        "utilization": billable_month / target if target else None,
        "month_value": round(value_between(data, month, today), 2),
        "running": running(data, now),
        "unbilled": unbilled(data, today),
    }


def last_12_months_billable(data: dict, today: date) -> int:
    return minutes_between(data, today - timedelta(days=365), today, kind=Hours.BILLABLE)
