"""Audit log: who changed what, from where."""

from datetime import timedelta

from sqlalchemy.orm import Session

from app.db.base import session_scope, utcnow
from app.db.models import AuditEntry
from app.repos import misc
from app.services.access import AccessDenied, Principal

RETENTION = timedelta(days=365)


def log(
    s: Session,
    user_id: int | None,
    action: str,
    target: str = "",
    ip: str = "",
    **detail,
) -> None:
    """Record inside the caller's transaction; never store secrets in detail."""
    misc.audit(s, AuditEntry(user_id=user_id, action=action, target=target, ip=ip, detail=detail))


def entries(who: Principal) -> list[AuditEntry]:
    if not who.is_admin:
        raise AccessDenied("audit")

    with session_scope() as s:
        return misc.audit_page(s)


def prune() -> None:
    with session_scope() as s:
        misc.prune_audit(s, utcnow() - RETENTION)
