"""Login sessions, API tokens, invitations, password resets."""

from datetime import datetime

from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from app.db.models import ApiToken, Invite, LoginSession, ResetToken


def session_by_hash(s: Session, token_hash: str) -> LoginSession | None:
    return s.scalar(select(LoginSession).where(LoginSession.token_hash == token_hash))


def sessions_of(s: Session, user_id: int) -> list[LoginSession]:
    stmt = (
        select(LoginSession)
        .where(LoginSession.user_id == user_id)
        .order_by(LoginSession.last_seen.desc())
    )
    return list(s.scalars(stmt))


def add(s: Session, item) -> None:
    s.add(item)
    s.flush()


def remove(s: Session, item) -> None:
    s.delete(item)


def drop_sessions(s: Session, user_id: int, keep_id: int | None = None) -> None:
    stmt = delete(LoginSession).where(LoginSession.user_id == user_id)
    if keep_id is not None:
        stmt = stmt.where(LoginSession.id != keep_id)
    s.execute(stmt)


def purge_expired(s: Session, now: datetime) -> None:
    s.execute(delete(LoginSession).where(LoginSession.expires_at < now))
    s.execute(delete(Invite).where(Invite.expires_at < now, Invite.used_at.is_(None)))
    s.execute(delete(ResetToken).where(ResetToken.expires_at < now))


def token_by_hash(s: Session, token_hash: str) -> ApiToken | None:
    return s.scalar(select(ApiToken).where(ApiToken.token_hash == token_hash))


def tokens_of(s: Session, user_id: int) -> list[ApiToken]:
    stmt = select(ApiToken).where(ApiToken.user_id == user_id).order_by(ApiToken.created_at)
    return list(s.scalars(stmt))


def token(s: Session, token_id: int) -> ApiToken | None:
    return s.get(ApiToken, token_id)


def invite_by_hash(s: Session, token_hash: str) -> Invite | None:
    return s.scalar(select(Invite).where(Invite.token_hash == token_hash))


def open_invites(s: Session) -> list[Invite]:
    stmt = select(Invite).where(Invite.used_at.is_(None)).order_by(Invite.expires_at)
    return list(s.scalars(stmt))


def invite(s: Session, invite_id: int) -> Invite | None:
    return s.get(Invite, invite_id)


def reset_by_hash(s: Session, token_hash: str) -> ResetToken | None:
    return s.scalar(select(ResetToken).where(ResetToken.token_hash == token_hash))
