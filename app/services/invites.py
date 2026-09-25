"""Invitations and password resets (one-time links by mail)."""

from dataclasses import dataclass
from datetime import timedelta

from app.db.base import session_scope, utcnow
from app.db.models import Invite, ResetToken
from app.enums import InstanceRole, Locale
from app.repos import auth as repo
from app.repos import users
from app.services import accounts, audit, crypto, mail
from app.services.access import AccessDenied, Principal
from app.settings import get_settings

INVITE_DAYS = 7
RESET_HOURS = 2


class InviteError(ValueError):
    pass


@dataclass
class InviteView:
    id: int
    email: str
    role: InstanceRole
    teams: list
    expires_at: object


def _aware(value):
    return value if value.tzinfo else value.replace(tzinfo=utcnow().tzinfo)


def create(who: Principal, email: str, role: InstanceRole, teams: list[dict], locale: Locale) -> str:
    """Admin invites a person; returns the link (also mailed if SMTP is set)."""
    if not who.is_admin:
        raise AccessDenied("invite")

    token = crypto.new_token()
    with session_scope() as s:
        if users.by_email(s, email.strip()):
            raise InviteError("account.email_taken")
        repo.add(
            s,
            Invite(
                email=email.strip(),
                token_hash=crypto.token_hash(token),
                role=role,
                teams=teams,
                created_by=who.user_id,
                expires_at=utcnow() + timedelta(days=INVITE_DAYS),
            ),
        )
        audit.log(s, who.user_id, "invite.created", target=email)

    link = f"{get_settings().base_url}/invite/{token}"
    mail.invite(email.strip(), link, who.name, locale)
    return link


def pending(who: Principal) -> list[InviteView]:
    if not who.is_admin:
        raise AccessDenied("invite")
    with session_scope() as s:
        return [InviteView(i.id, i.email, InstanceRole(i.role), i.teams, i.expires_at)
                for i in repo.open_invites(s)]


def revoke(who: Principal, invite_id: int) -> None:
    if not who.is_admin:
        raise AccessDenied("invite")
    with session_scope() as s:
        item = repo.invite(s, invite_id)
        if item:
            repo.remove(s, item)


def peek(token: str) -> InviteView | None:
    with session_scope() as s:
        item = repo.invite_by_hash(s, crypto.token_hash(token))
        if item is None or item.used_at or _aware(item.expires_at) < utcnow():
            return None
        return InviteView(item.id, item.email, InstanceRole(item.role), item.teams, item.expires_at)


def accept(token: str, name: str, password: str, locale: Locale) -> str:
    with session_scope() as s:
        item = repo.invite_by_hash(s, crypto.token_hash(token))
        if item is None or item.used_at or _aware(item.expires_at) < utcnow():
            raise InviteError("invite.invalid")

        user = accounts.create(s, item.email, name, password, InstanceRole(item.role), locale)
        accounts.join_teams(s, user.id, list(item.teams or []))
        item.used_at = utcnow()
        audit.log(s, user.id, "invite.accepted", target=item.email)
        return user.email


def request_reset(email: str, ip: str) -> None:
    """Always looks the same to the requester, whether the account exists or not."""
    token = crypto.new_token()
    with session_scope() as s:
        user = users.by_email(s, email.strip())
        if user is None or not user.is_active or not user.password_hash:
            return
        repo.add(s, ResetToken(user_id=user.id, token_hash=crypto.token_hash(token),
                               expires_at=utcnow() + timedelta(hours=RESET_HOURS)))
        audit.log(s, user.id, "reset.requested", ip=ip)
        address, locale = user.email, user.locale

    mail.reset(address, f"{get_settings().base_url}/reset/{token}", Locale(locale))


def admin_reset_link(who: Principal, user_id: int) -> str:
    """Admin creates a reset link for a user (no SMTP needed)."""
    if not who.is_admin:
        raise AccessDenied("reset")
    token = crypto.new_token()
    with session_scope() as s:
        repo.add(s, ResetToken(user_id=user_id, token_hash=crypto.token_hash(token),
                               expires_at=utcnow() + timedelta(hours=RESET_HOURS * 12)))
        audit.log(s, who.user_id, "reset.admin_link", target=str(user_id))
    return f"{get_settings().base_url}/reset/{token}"


def reset_valid(token: str) -> bool:
    with session_scope() as s:
        item = repo.reset_by_hash(s, crypto.token_hash(token))
        return bool(item and not item.used_at and _aware(item.expires_at) > utcnow())


def reset(token: str, password: str, ip: str) -> None:
    accounts.check_password_rules(password)
    with session_scope() as s:
        item = repo.reset_by_hash(s, crypto.token_hash(token))
        if item is None or item.used_at or _aware(item.expires_at) < utcnow():
            raise InviteError("reset.invalid")

        user = users.get(s, item.user_id)
        user.password_hash = crypto.hash_password(password)
        item.used_at = utcnow()
        repo.drop_sessions(s, user.id)
        audit.log(s, user.id, "reset.done", ip=ip)
        user_id = user.id

    mail.security_notice(user_id, "password_changed")
