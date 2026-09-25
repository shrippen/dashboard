"""Source cache, metric points, hints, notifications."""

from datetime import datetime

from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from app.db.models import CacheEntry, Hint, HintMark, MetricPoint, NotifyChannel, NotifyLog

# ── Cache ──


def cache(s: Session, key: str) -> CacheEntry | None:
    return s.get(CacheEntry, key)


def put_cache(s: Session, entry: CacheEntry) -> None:
    s.merge(entry)


def prune_cache(s: Session, older_than: datetime) -> None:
    s.execute(delete(CacheEntry).where(CacheEntry.fetched_at < older_than))


# ── Metrics ──


def put_point(s: Session, scope: str, metric: str, day: str, value: float) -> None:
    stmt = select(MetricPoint).where(
        MetricPoint.scope == scope, MetricPoint.metric == metric, MetricPoint.day == day
    )
    item = s.scalar(stmt)
    if item is None:
        s.add(MetricPoint(scope=scope, metric=metric, day=day, value=value))
        return

    item.value = value


def points(s: Session, scope: str, metric: str, since: str) -> list[MetricPoint]:
    stmt = (
        select(MetricPoint)
        .where(MetricPoint.scope == scope, MetricPoint.metric == metric, MetricPoint.day >= since)
        .order_by(MetricPoint.day)
    )
    return list(s.scalars(stmt))


def prune_points(s: Session, before_day: str) -> None:
    s.execute(delete(MetricPoint).where(MetricPoint.day < before_day))


# ── Hints ──


def hints_in(s: Session, space_ids: list[int], user_id: int) -> list[Hint]:
    """Active hints of the given spaces: shared ones plus the user's own."""
    stmt = select(Hint).where(
        Hint.space_id.in_(space_ids),
        Hint.resolved_at.is_(None),
        (Hint.user_id.is_(None)) | (Hint.user_id == user_id),
    )
    return list(s.scalars(stmt))


def hint(s: Session, hint_id: int) -> Hint | None:
    return s.get(Hint, hint_id)


def hints_of_scope(
    s: Session, space_id: int, user_id: int | None, rules: list[str], conn_id: int | None
) -> list[Hint]:
    stmt = select(Hint).where(
        Hint.space_id == space_id,
        Hint.rule.in_(rules),
        Hint.resolved_at.is_(None),
    )
    stmt = stmt.where(Hint.user_id.is_(None) if user_id is None else Hint.user_id == user_id)
    if conn_id is not None:
        stmt = stmt.where(Hint.connection_id == conn_id)
    return list(s.scalars(stmt))


def hint_by_print(s: Session, space_id: int, user_id: int | None, fingerprint: str) -> Hint | None:
    stmt = select(Hint).where(Hint.space_id == space_id, Hint.fingerprint == fingerprint)
    stmt = stmt.where(Hint.user_id.is_(None) if user_id is None else Hint.user_id == user_id)
    return s.scalar(stmt)


def add(s: Session, item):
    s.add(item)
    s.flush()
    return item


def remove(s: Session, item) -> None:
    s.delete(item)


def marks(s: Session, hint_ids: list[int], user_id: int) -> list[HintMark]:
    if not hint_ids:
        return []

    stmt = select(HintMark).where(
        HintMark.hint_id.in_(hint_ids),
        (HintMark.user_id.is_(None)) | (HintMark.user_id == user_id),
    )
    return list(s.scalars(stmt))


def mark(s: Session, hint_id: int, user_id: int | None) -> HintMark | None:
    stmt = select(HintMark).where(HintMark.hint_id == hint_id)
    stmt = stmt.where(HintMark.user_id.is_(None) if user_id is None else HintMark.user_id == user_id)
    return s.scalar(stmt)


def drop_marks(s: Session, hint_id: int) -> None:
    s.execute(delete(HintMark).where(HintMark.hint_id == hint_id))


def prune_resolved(s: Session, older_than: datetime) -> None:
    s.execute(delete(Hint).where(Hint.resolved_at < older_than))


# ── Notifications ──


def channels(s: Session, user_id: int) -> list[NotifyChannel]:
    return list(s.scalars(select(NotifyChannel).where(NotifyChannel.user_id == user_id)))


def channel(s: Session, channel_id: int) -> NotifyChannel | None:
    return s.get(NotifyChannel, channel_id)


def was_sent(s: Session, user_id: int, hint_id: int) -> bool:
    stmt = select(NotifyLog.id).where(NotifyLog.user_id == user_id, NotifyLog.hint_id == hint_id)
    return s.scalar(stmt) is not None


def log_sent(s: Session, user_id: int, hint_id: int) -> None:
    s.add(NotifyLog(user_id=user_id, hint_id=hint_id))
