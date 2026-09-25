"""Hints: store rule findings, reconcile them per run, let users act on them.

    rule run (space, user?, connection)
        findings ──► upsert by fingerprint ──► missing ones: resolved
                                                reappearing: reopened, marks dropped
    reader
        open hints of reachable spaces − acknowledged − snoozed(until > now)
"""

from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum

from app.db.base import session_scope, utcnow
from app.db.models import Hint, HintMark
from app.enums import HintAckMode, HintState, Right, Severity, SpaceKind
from app.repos import content
from app.repos import data as repo
from app.rules.base import Finding
from app.services import access, i18n
from app.services.access import AccessDenied, Principal
from app.services.i18n import t
from app.services.util import NotFound

RESOLVED_RETENTION = timedelta(days=90)
ACK_MODE = "hint_ack"


@dataclass
class HintView:
    id: int
    rule: str
    severity: Severity
    title: str
    why: str
    action_url: str | None
    action_label: str | None
    due: str | None
    sources: list[str]
    space_name: str
    first_seen: datetime
    connection_id: int | None


class HintAction(StrEnum):
    ACK = "ack"
    SNOOZE = "snooze"
    REOPEN = "reopen"


# ── Reconcile ──


def sync(
    space_id: int,
    user_id: int | None,
    conn_id: int | None,
    rules: list[str],
    findings: list[Finding],
) -> int:
    """Apply one rule run. Returns the number of hints that are new or reopened."""
    now = utcnow()
    fresh: list[int] = []
    with session_scope() as s:
        seen = set()
        for item in findings:
            # Two connections of one service in a space must not share fingerprints.
            fingerprint = f"{conn_id}:{item.fingerprint}" if conn_id else item.fingerprint
            seen.add(fingerprint)
            hint = repo.hint_by_print(s, space_id, user_id, fingerprint)
            if hint is None:
                hint = Hint(space_id=space_id, user_id=user_id, fingerprint=fingerprint, first_seen=now)
                _fill(hint, item, conn_id, now)
                fresh.append(repo.add(s, hint).id)
                continue

            if hint.resolved_at is not None:
                hint.resolved_at = None
                hint.first_seen = now
                repo.drop_marks(s, hint.id)
                fresh.append(hint.id)
            _fill(hint, item, conn_id, now)

        for hint in repo.hints_of_scope(s, space_id, user_id, rules, conn_id):
            if hint.fingerprint not in seen:
                hint.resolved_at = now

    return len(fresh)


def _fill(hint: Hint, item: Finding, conn_id: int | None, now: datetime) -> None:
    hint.rule = item.rule
    hint.severity = int(item.severity)
    hint.message = item.message
    hint.params = item.params
    hint.action_url = item.action_url
    hint.action_label = item.action_label
    hint.due = item.due
    hint.sources = item.sources
    hint.connection_id = conn_id
    hint.last_seen = now


# ── Read ──


def _aware(value: datetime | None) -> datetime | None:
    if value is None or value.tzinfo:
        return value
    return value.replace(tzinfo=utcnow().tzinfo)


def _hidden(marks: list[HintMark], now: datetime) -> bool:
    for mark in marks:
        if mark.state == HintState.ACKNOWLEDGED:
            return True
        if mark.state == HintState.SNOOZED and mark.until and _aware(mark.until) > now:
            return True
    return False


def _visible(who: Principal, s) -> list[Hint]:
    found = repo.hints_in(s, list(who.spaces), who.user_id)
    now = utcnow()
    by_hint: dict[int, list[HintMark]] = {}
    for mark in repo.marks(s, [h.id for h in found], who.user_id):
        by_hint.setdefault(mark.hint_id, []).append(mark)

    return [h for h in found if not _hidden(by_hint.get(h.id, []), now)]


def active(
    who: Principal,
    min_severity: Severity = Severity.INFO,
    sources: list[str] | None = None,
    limit: int | None = None,
) -> list[HintView]:
    with session_scope() as s:
        rows = [h for h in _visible(who, s) if h.severity >= min_severity]
        if sources:
            rows = [h for h in rows if set(h.sources) & set(sources)]

        rows.sort(key=lambda h: (-h.severity, h.due or "9999", h.first_seen))
        if limit:
            rows = rows[:limit]

        return [_view(h, who) for h in rows]


def _view(hint: Hint, who: Principal) -> HintView:
    locale = who.locale
    params = i18n.typed(hint.params or {}, locale)
    space = who.spaces.get(hint.space_id)
    return HintView(
        id=hint.id,
        rule=hint.rule,
        severity=Severity(hint.severity),
        title=t(f"hint.{hint.message}.title", locale, **params),
        why=t(f"hint.{hint.message}.why", locale, **params),
        action_url=hint.action_url,
        action_label=t(f"action.{hint.action_label}", locale) if hint.action_label else None,
        due=hint.due,
        sources=list(hint.sources or []),
        space_name=space.name if space else "",
        first_seen=_aware(hint.first_seen),
        connection_id=hint.connection_id,
    )


def count_for(who: Principal, conn_id: int) -> tuple[int, int]:
    """Open hints of one connection: (count, highest severity)."""
    with session_scope() as s:
        rows = [h for h in _visible(who, s) if h.connection_id == conn_id]
        return len(rows), max((h.severity for h in rows), default=0)


def summary(who: Principal) -> dict[Severity, int]:
    with session_scope() as s:
        counts = {level: 0 for level in Severity}
        for hint in _visible(who, s):
            counts[Severity(hint.severity)] += 1
        return counts


# ── Act ──


def _ack_mode(s, space_id: int) -> HintAckMode:
    space = content.space(s, space_id)
    if space is None or space.kind != SpaceKind.TEAM:
        return HintAckMode.PER_USER
    return HintAckMode((space.settings or {}).get(ACK_MODE, HintAckMode.PER_USER))


def mark(who: Principal, hint_id: int, state: HintState, until: datetime | None = None) -> None:
    with session_scope() as s:
        hint = repo.hint(s, hint_id)
        if hint is None:
            raise NotFound("hint")
        if hint.space_id not in who.spaces or hint.user_id not in (None, who.user_id):
            raise AccessDenied("hint")

        team_wide = _ack_mode(s, hint.space_id) == HintAckMode.TEAM
        if team_wide and state == HintState.ACKNOWLEDGED:
            space = access.space_of(s, who, hint.space_id)
            if access.space_right(who, space) < Right.EDIT:
                team_wide = False

        owner = None if team_wide else who.user_id
        existing = repo.mark(s, hint_id, owner)
        if state == HintState.OPEN:
            if existing:
                repo.remove(s, existing)
            return

        if existing is None:
            existing = repo.add(s, HintMark(hint_id=hint_id, user_id=owner, state=state))
        existing.state = state
        existing.until = until
        existing.at = utcnow()


def act(who: Principal, hint_id: int, action: HintAction, days: int = 7) -> None:
    if action == HintAction.ACK:
        mark(who, hint_id, HintState.ACKNOWLEDGED)
    elif action == HintAction.SNOOZE:
        mark(who, hint_id, HintState.SNOOZED, utcnow() + timedelta(days=max(1, days)))
    else:
        mark(who, hint_id, HintState.OPEN)


def prune() -> None:
    with session_scope() as s:
        repo.prune_resolved(s, utcnow() - RESOLVED_RETENTION)
