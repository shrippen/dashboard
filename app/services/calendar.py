"""iCal feed: tax deadlines and hints with a due date, per user (token protected)."""

from datetime import date, datetime, timedelta

from app.db.base import session_scope, utcnow
from app.metrics import deadlines as dl
from app.repos import content
from app.services import hints
from app.services.access import Principal
from app.services.i18n import t

HORIZON_DAYS = 400
PRODID = "-//shrippen//dashboard//DE"


def _escape(text: str) -> str:
    return text.replace("\\", "\\\\").replace(";", r"\;").replace(",", "\\,").replace("\n", "\\n")


def _event(uid: str, day: date, summary: str, stamp: str) -> list[str]:
    return [
        "BEGIN:VEVENT",
        f"UID:{uid}@dashboard",
        f"DTSTAMP:{stamp}",
        f"DTSTART;VALUE=DATE:{day.strftime('%Y%m%d')}",
        f"DTEND;VALUE=DATE:{(day + timedelta(days=1)).strftime('%Y%m%d')}",
        f"SUMMARY:{_escape(summary)}",
        "END:VEVENT",
    ]


def feed(who: Principal) -> str:
    today = date.today()
    stamp = utcnow().strftime("%Y%m%dT%H%M%SZ")
    lines = ["BEGIN:VCALENDAR", "VERSION:2.0", f"PRODID:{PRODID}", "CALSCALE:GREGORIAN",
             f"X-WR-CALNAME:{t('calendar.name', who.locale)}"]

    with session_scope() as s:
        settings = [(sp.id, dict(sp.settings or {})) for sp in content.spaces(s, list(who.spaces))]

    for space_id, space_settings in settings:
        for item in dl.upcoming(space_settings, today, HORIZON_DAYS):
            text = t(f"deadline.{item['kind']}", who.locale, period=item.get("period", ""), year=item.get("year", ""))
            lines += _event(f"{space_id}-{item['kind']}-{item['due'].isoformat()}", item["due"], text, stamp)

    for hint in hints.active(who):
        if hint.due and not hint.rule.startswith("tax."):
            lines += _event(f"hint-{hint.id}", datetime.fromisoformat(hint.due).date(), hint.title, stamp)

    lines.append("END:VCALENDAR")
    return "\r\n".join(lines) + "\r\n"
