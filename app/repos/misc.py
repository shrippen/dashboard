"""Shares, themes, instance settings, audit log."""

from datetime import datetime

from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from app.db.models import (
    AuditEntry,
    Connection,
    InstanceSetting,
    NotifyChannel,
    Share,
    Theme,
    User,
    UserCredential,
)
from app.enums import GranteeKind, ResourceKind

AUDIT_PAGE = 200


# ── Shares ──


def shares_for(s: Session, kind: ResourceKind, resource_id: int) -> list[Share]:
    stmt = select(Share).where(Share.resource_kind == kind, Share.resource_id == resource_id)
    return list(s.scalars(stmt))


def shares_to(s: Session, user_id: int, team_ids: list[int]) -> list[Share]:
    user_match = (Share.grantee_kind == GranteeKind.USER) & (Share.grantee_id == user_id)
    team_match = (Share.grantee_kind == GranteeKind.TEAM) & (Share.grantee_id.in_(team_ids))
    return list(s.scalars(select(Share).where(user_match | team_match)))


def share(s: Session, share_id: int) -> Share | None:
    return s.get(Share, share_id)


def add(s: Session, item):
    s.add(item)
    s.flush()
    return item


def remove(s: Session, item) -> None:
    s.delete(item)


def drop_shares(s: Session, kind: ResourceKind, resource_id: int) -> None:
    stmt = delete(Share).where(Share.resource_kind == kind, Share.resource_id == resource_id)
    s.execute(stmt)


# ── Themes ──


def theme(s: Session, theme_id: int) -> Theme | None:
    return s.get(Theme, theme_id)


def builtin_theme(s: Session, slug: str) -> Theme | None:
    return s.scalar(select(Theme).where(Theme.builtin.is_(True), Theme.slug == slug))


def themes(s: Session, space_ids: list[int]) -> list[Theme]:
    visible = Theme.builtin.is_(True) | Theme.space_id.in_(space_ids)
    return list(s.scalars(select(Theme).where(visible).order_by(Theme.name)))


# ── Instance settings ──


def setting(s: Session, key: str) -> dict:
    item = s.get(InstanceSetting, key)
    return dict(item.value) if item else {}


def set_setting(s: Session, key: str, value: dict) -> None:
    item = s.get(InstanceSetting, key)
    if item is None:
        s.add(InstanceSetting(key=key, value=value))
        return

    item.value = value


# ── Audit ──


def audit(s: Session, entry: AuditEntry) -> None:
    s.add(entry)


def audit_page(s: Session, before: datetime | None = None) -> list[AuditEntry]:
    stmt = select(AuditEntry).order_by(AuditEntry.at.desc()).limit(AUDIT_PAGE)
    if before is not None:
        stmt = stmt.where(AuditEntry.at < before)
    return list(s.scalars(stmt))


def prune_audit(s: Session, older_than: datetime) -> None:
    s.execute(delete(AuditEntry).where(AuditEntry.at < older_than))


def encrypted_rows(s: Session) -> dict[str, list]:
    """Every row holding an encrypted value (key rotation)."""
    return {
        "connections": list(s.scalars(select(Connection))),
        "credentials": list(s.scalars(select(UserCredential))),
        "users": list(s.scalars(select(User))),
        "channels": list(s.scalars(select(NotifyChannel))),
    }
