"""Account administration. Admins see accounts, never personal content."""

from dataclasses import dataclass
from enum import StrEnum

from app.db.base import session_scope
from app.enums import InstanceRole, Locale, TeamRole
from app.repos import auth as auth_repo
from app.repos import misc, users
from app.services import accounts, audit
from app.services.access import AccessDenied, Principal

REGISTRATION_KEY = "registration"


class AdminError(ValueError):
    pass


@dataclass
class UserRow:
    id: int
    email: str
    name: str
    role: InstanceRole
    active: bool
    breakglass: bool
    totp: bool
    oidc: bool
    teams: list[tuple[str, TeamRole]]
    last_login: object


def _need(who: Principal) -> None:
    if not who.is_admin:
        raise AccessDenied("admin")


def user_rows(who: Principal) -> list[UserRow]:
    _need(who)
    with session_scope() as s:
        team_names = {t.id: t.name for t in users.teams(s)}
        return [
            UserRow(
                u.id, u.email, u.name, InstanceRole(u.role), u.is_active, u.is_breakglass,
                u.totp_enabled, bool(u.oidc_sub),
                [(team_names.get(m.team_id, "?"), TeamRole(m.role)) for m in u.memberships],
                u.last_login_at,
            )
            for u in users.all_users(s)
        ]


def set_role(who: Principal, user_id: int, role: InstanceRole, ip: str) -> None:
    _need(who)
    with session_scope() as s:
        user = users.get(s, user_id)
        if user is None:
            return
        if user.role == InstanceRole.ADMIN and role != InstanceRole.ADMIN and users.count_admins(s) <= 1:
            raise AdminError("admin.last_admin")
        user.role = role
        audit.log(s, who.user_id, "user.role", target=user.email, ip=ip, role=role.value)


class Switch(StrEnum):
    ON = "on"
    OFF = "off"


def set_active(who: Principal, user_id: int, active: Switch, ip: str) -> None:
    _need(who)
    with session_scope() as s:
        user = users.get(s, user_id)
        if user is None:
            return
        turn_on = active == Switch.ON
        if user.id == who.user_id and not turn_on:
            raise AdminError("admin.self")
        if user.role == InstanceRole.ADMIN and not turn_on and users.count_admins(s) <= 1:
            raise AdminError("admin.last_admin")
        user.is_active = turn_on
        if not turn_on:
            auth_repo.drop_sessions(s, user.id)
        audit.log(s, who.user_id, "user.active", target=user.email, ip=ip, active=turn_on)


def set_breakglass(who: Principal, user_id: int, flag: Switch, ip: str) -> None:
    _need(who)
    with session_scope() as s:
        user = users.get(s, user_id)
        if user is None:
            return
        user.is_breakglass = flag == Switch.ON
        audit.log(s, who.user_id, "user.breakglass", target=user.email, ip=ip, on=user.is_breakglass)


def delete_user(who: Principal, user_id: int, ip: str) -> None:
    """Deletes the account and its personal space with all content."""
    _need(who)
    with session_scope() as s:
        user = users.get(s, user_id)
        if user is None:
            return
        if user.id == who.user_id:
            raise AdminError("admin.self")
        if user.role == InstanceRole.ADMIN and users.count_admins(s) <= 1:
            raise AdminError("admin.last_admin")
        audit.log(s, who.user_id, "user.deleted", target=user.email, ip=ip)
        users.delete(s, user)


def registration_open() -> bool:
    with session_scope() as s:
        return bool(misc.setting(s, REGISTRATION_KEY).get("open"))


def register(email: str, name: str, password: str, locale: Locale) -> str:
    """Self-registration (off by default, admin switch)."""
    if not registration_open():
        raise AdminError("register.closed")
    with session_scope() as s:
        user = accounts.create(s, email, name, password, InstanceRole.USER, locale)
        audit.log(s, user.id, "user.registered", target=user.email)
        return user.email
