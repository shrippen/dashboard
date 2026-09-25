"""Accounts: create users with their personal space, profile changes."""

from dataclasses import dataclass

from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Space, User
from app.enums import ColorMode, InstanceRole, Locale, SpaceKind, TeamRole
from app.repos import content, users
from app.services import audit, crypto, mail
from app.services import teams as team_service
from app.services.access import Principal

MIN_PASSWORD = 12


class AccountError(ValueError):
    pass


@dataclass
class Profile:
    id: int
    email: str
    name: str
    role: InstanceRole
    locale: Locale
    color_mode: ColorMode
    theme_id: int | None
    start_board_id: int | None
    search_engine: str | None
    totp_enabled: bool
    has_password: bool
    oidc_linked: bool
    prefs: dict


def check_password_rules(password: str) -> None:
    if len(password) < MIN_PASSWORD:
        raise AccountError("password.too_short")


def create(
    s: Session,
    email: str,
    name: str,
    password: str | None,
    role: InstanceRole = InstanceRole.USER,
    locale: Locale = Locale.DE,
    sub: str | None = None,
) -> User:
    """User plus personal space. Caller owns the transaction."""
    email = email.strip()
    if users.by_email(s, email):
        raise AccountError("account.email_taken")

    if password is not None:
        check_password_rules(password)

    user = users.add(
        s,
        User(
            email=email,
            name=name.strip() or email,
            password_hash=crypto.hash_password(password) if password else None,
            role=role,
            locale=locale,
            oidc_sub=sub,
        ),
    )
    content.add(s, Space(kind=SpaceKind.PERSONAL, name=user.name, owner_user_id=user.id))
    return user


def join_teams(s: Session, user_id: int, teams: list[dict]) -> None:
    """teams: [{"team": name, "role": "editor"}]; unknown teams are created."""
    for item in teams:
        team = users.team_by_name(s, item["team"]) or team_service.create_in(s, item["team"])
        users.set_member(s, user_id, team.id, TeamRole(item.get("role", TeamRole.VIEWER)))


def profile(who: Principal) -> Profile:
    with session_scope() as s:
        user = users.get(s, who.user_id)
        return Profile(
            id=user.id,
            email=user.email,
            name=user.name,
            role=InstanceRole(user.role),
            locale=Locale(user.locale),
            color_mode=ColorMode(user.color_mode),
            theme_id=user.theme_id,
            start_board_id=user.start_board_id,
            search_engine=user.search_engine,
            totp_enabled=user.totp_enabled,
            has_password=bool(user.password_hash),
            oidc_linked=bool(user.oidc_sub),
            prefs=dict(user.prefs or {}),
        )


def update_profile(who: Principal, **changes) -> None:
    allowed = {"name", "locale", "color_mode", "theme_id", "start_board_id", "search_engine"}
    with session_scope() as s:
        user = users.get(s, who.user_id)
        for key, value in changes.items():
            if key not in allowed:
                continue
            setattr(user, key, value)


def set_pref(who: Principal, key: str, value) -> None:
    with session_scope() as s:
        user = users.get(s, who.user_id)
        prefs = dict(user.prefs or {})
        prefs[key] = value
        user.prefs = prefs


def change_password(who: Principal, current: str | None, new: str, ip: str) -> None:
    check_password_rules(new)
    with session_scope() as s:
        user = users.get(s, who.user_id)
        if user.password_hash and not crypto.check_password(user.password_hash, current or ""):
            raise AccountError("password.wrong")

        user.password_hash = crypto.hash_password(new)
        audit.log(s, who.user_id, "password.changed", ip=ip)

    mail.security_notice(who.user_id, "password_changed")
