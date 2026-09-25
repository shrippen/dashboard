"""Date helpers shared by metrics and rules."""

from datetime import date, datetime, timedelta

WEEKEND = (5, 6)


def parse_day(value) -> date | None:
    if not value:
        return None
    if isinstance(value, date):
        return value
    return date.fromisoformat(str(value)[:10])


def parse_time(value) -> datetime | None:
    if not value:
        return None
    return datetime.fromisoformat(str(value).replace("Z", "+00:00"))


def month_start(day: date) -> date:
    return day.replace(day=1)


def add_months(day: date, months: int) -> date:
    """1st of the month `months` away: add_months(2026-03-15, -2) → 2026-01-01."""
    index = day.year * 12 + day.month - 1 + months
    return date(index // 12, index % 12 + 1, 1)


def week_start(day: date) -> date:
    return day - timedelta(days=day.weekday())


def quarter_start(day: date) -> date:
    return date(day.year, (day.month - 1) // 3 * 3 + 1, 1)


def workdays(start: date, end: date, free: set[date]) -> list[date]:
    """Mon–Fri between start and end (inclusive), minus holidays/absences."""
    found = []
    day = start
    while day <= end:
        if day.weekday() not in WEEKEND and day not in free:
            found.append(day)
        day += timedelta(days=1)
    return found


def expand(start, end) -> set[date]:
    first, last = parse_day(start), parse_day(end) or parse_day(start)
    if first is None:
        return set()
    return {first + timedelta(days=i) for i in range((last - first).days + 1)}
