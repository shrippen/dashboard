"""Notifications per user: Apprise channels, quiet hours, daily/weekly digest mail.

    every minute   new hints ≥ channel level, not sent yet, outside quiet hours → push
    every 5 min    digest time reached, not sent today → HTML mail (SMTP)
"""

import logging
from dataclasses import dataclass
from datetime import date, datetime, time
from zoneinfo import ZoneInfo

from app.db.base import session_scope, utcnow
from app.db.models import NotifyChannel
from app.enums import Locale, Severity
from app.metrics import deadlines as dl
from app.outbound import notify as outbound
from app.repos import content, users
from app.repos import data as repo
from app.services import access, audit, crypto, hints, mail
from app.services.access import AccessDenied, Principal
from app.services.crypto import Purpose
from app.services.i18n import t
from app.services.mail import Row

log = logging.getLogger(__name__)

TZ = ZoneInfo("Europe/Berlin")
QUIET = "quiet"
DIGEST = "digest"
DIGEST_SENT = "digest_sent"
MAX_LINES = 10
DEADLINE_DAYS = 14
WEEKDAYS = ("mon", "tue", "wed", "thu", "fri", "sat", "sun")
LEVEL_COLOR = {Severity.INFO: "#83a598", Severity.WARN: "#fabd2f", Severity.CRITICAL: "#fb4934"}


class NotifyError(ValueError):
    pass


@dataclass
class ChannelView:
    id: int
    name: str
    hint: str
    min_severity: Severity
    enabled: bool


def _mask(url: str) -> str:
    """ntfy://ntfy.lan/topic → ntfy://…/topic (no credentials on screen)."""
    scheme, _sep, rest = url.partition("://")
    return f"{scheme}://…/{rest.rsplit('/', 1)[-1]}" if rest else scheme


def channels(who: Principal) -> list[ChannelView]:
    with session_scope() as s:
        return [
            ChannelView(c.id, c.name, _mask(crypto.decrypt(c.url_enc, Purpose.NOTIFY)), Severity(c.min_severity),
                        c.enabled)
            for c in repo.channels(s, who.user_id)
        ]


def add_channel(who: Principal, name: str, url: str, level: Severity) -> None:
    url = url.strip()
    if not outbound.valid(url):
        raise NotifyError("notify.invalid_url")
    with session_scope() as s:
        repo.add(s, NotifyChannel(user_id=who.user_id, name=name.strip() or url.split(":")[0],
                                  url_enc=crypto.encrypt(url, Purpose.NOTIFY), min_severity=int(level)))
        audit.log(s, who.user_id, "notify.channel_added", target=name)


def _own(s, who: Principal, channel_id: int) -> NotifyChannel:
    item = repo.channel(s, channel_id)
    if item is None or item.user_id != who.user_id:
        raise AccessDenied("channel")
    return item


def delete_channel(who: Principal, channel_id: int) -> None:
    with session_scope() as s:
        repo.remove(s, _own(s, who, channel_id))


def test_channel(who: Principal, channel_id: int) -> None:
    with session_scope() as s:
        url = crypto.decrypt(_own(s, who, channel_id).url_enc, Purpose.NOTIFY)
    try:
        outbound.send(url, t("notify.test_title", who.locale), t("notify.test_body", who.locale))
    except outbound.NotifyFailed as exc:
        raise NotifyError("notify.failed") from exc


# ── Preferences ──


@dataclass
class Prefs:
    quiet_from: str
    quiet_to: str
    daily: str
    weekly: str


def prefs(who: Principal) -> Prefs:
    with session_scope() as s:
        raw = dict(users.get(s, who.user_id).prefs or {})
    quiet, digest = raw.get(QUIET) or {}, raw.get(DIGEST) or {}
    return Prefs(quiet.get("from", ""), quiet.get("to", ""), digest.get("daily", ""), digest.get("weekly", ""))


def save_prefs(who: Principal, value: Prefs) -> None:
    for text in (value.quiet_from, value.quiet_to, value.daily):
        if text and not _clock(text):
            raise NotifyError("notify.bad_time")
    if value.weekly and value.weekly not in WEEKDAYS:
        raise NotifyError("notify.bad_time")

    with session_scope() as s:
        user = users.get(s, who.user_id)
        raw = dict(user.prefs or {})
        raw[QUIET] = {"from": value.quiet_from, "to": value.quiet_to}
        raw[DIGEST] = {"daily": value.daily, "weekly": value.weekly}
        user.prefs = raw


def _clock(text: str) -> time | None:
    try:
        return time.fromisoformat(text)
    except ValueError:
        return None


def quiet_now(raw: dict, now: datetime) -> bool:
    """Quiet window may cross midnight: 22:00–07:00."""
    quiet = raw.get(QUIET) or {}
    start, end = _clock(quiet.get("from", "")), _clock(quiet.get("to", ""))
    if not start or not end:
        return False
    current = now.astimezone(TZ).time()
    if start <= end:
        return start <= current < end
    return current >= start or current < end


# ── Dispatch (jobs) ──


def dispatch() -> int:
    """Push new hints to every user's channels. Returns the number of messages."""
    sent = 0
    now = utcnow()
    with session_scope() as s:
        people = [(u.id, dict(u.prefs or {})) for u in users.all_users(s) if u.is_active]

    for user_id, raw in people:
        if quiet_now(raw, now):
            continue
        sent += _dispatch_user(user_id)
    return sent


def _dispatch_user(user_id: int) -> int:
    with session_scope() as s:
        who = access.principal(s, user_id)
        chans = [(c.id, c.name, crypto.decrypt(c.url_enc, Purpose.NOTIFY), c.min_severity)
                 for c in repo.channels(s, user_id) if c.enabled]
    if who is None or not chans:
        return 0

    open_hints = hints.active(who)
    with session_scope() as s:
        new = [h for h in open_hints if not repo.was_sent(s, user_id, h.id)]
    if not new:
        return 0

    sent = 0
    for _cid, name, url, level in chans:
        batch = [h for h in new if h.severity >= level]
        if not batch:
            continue
        title = t("notify.title", who.locale, count=len(batch))
        body = "\n".join(f"• {h.title}" for h in batch[:MAX_LINES])
        try:
            outbound.send(url, title, body)
            sent += 1
        except outbound.NotifyFailed:
            log.warning("channel %s of user %s failed", name, user_id)

    with session_scope() as s:
        for hint in new:
            repo.log_sent(s, user_id, hint.id)
    return sent


def digests(now: datetime | None = None) -> int:
    """Send due digest mails (daily at HH:MM, weekly on the chosen weekday at the same time)."""
    now = (now or utcnow()).astimezone(TZ)
    sent = 0
    with session_scope() as s:
        people = [(u.id, dict(u.prefs or {})) for u in users.all_users(s) if u.is_active]

    for user_id, raw in people:
        digest = raw.get(DIGEST) or {}
        at = _clock(digest.get("daily") or "")
        if at is None or now.time() < at or raw.get(DIGEST_SENT) == now.date().isoformat():
            continue
        weekly = digest.get("weekly")
        if weekly and WEEKDAYS[now.weekday()] != weekly:
            continue
        _send_digest(user_id, now.date())
        sent += 1
    return sent


def _send_digest(user_id: int, today: date) -> None:
    with session_scope() as s:
        who = access.principal(s, user_id)
        user = users.get(s, user_id)
        prefs_ = dict(user.prefs or {})
        prefs_[DIGEST_SENT] = today.isoformat()
        user.prefs = prefs_
        settings = [dict(sp.settings or {}) for sp in content.spaces(s, list(who.spaces))]

    locale = who.locale
    rows = [Row(t(f"severity.{h.severity.name.lower()}", locale), h.title, LEVEL_COLOR[h.severity])
            for h in hints.active(who)[:MAX_LINES * 2]]
    for space_settings in settings:
        for item in dl.upcoming(space_settings, today, DEADLINE_DAYS):
            text = t(f"deadline.{item['kind']}", locale, period=item.get("period", ""), year=item.get("year", ""))
            rows.append(Row(item["due"].isoformat(), text))

    subject = t("notify.digest_subject", locale, count=len(rows))
    body = t("notify.digest_body", locale) if rows else t("hints.none", locale)
    mail.send(mail.render(who.email, Locale(locale), subject, [body], items=rows))
