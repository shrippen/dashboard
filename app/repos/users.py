"""Users, teams and memberships."""

from sqlalchemy import func, select
from sqlalchemy.orm import Session, selectinload

from app.db.models import Membership, Team, User
from app.enums import InstanceRole, TeamRole


def get(s: Session, user_id: int) -> User | None:
    return s.get(User, user_id)


def by_email(s: Session, email: str) -> User | None:
    return s.scalar(select(User).where(func.lower(User.email) == email.lower()))


def by_sub(s: Session, sub: str) -> User | None:
    return s.scalar(select(User).where(User.oidc_sub == sub))


def all_users(s: Session) -> list[User]:
    stmt = select(User).options(selectinload(User.memberships)).order_by(User.name)
    return list(s.scalars(stmt))


def count(s: Session) -> int:
    return s.scalar(select(func.count(User.id))) or 0


def count_admins(s: Session) -> int:
    stmt = select(func.count(User.id)).where(
        User.role == InstanceRole.ADMIN, User.is_active.is_(True)
    )
    return s.scalar(stmt) or 0


def add(s: Session, user: User) -> User:
    s.add(user)
    s.flush()
    return user


def delete(s: Session, user: User) -> None:
    s.delete(user)


def team(s: Session, team_id: int) -> Team | None:
    return s.get(Team, team_id)


def team_by_name(s: Session, name: str) -> Team | None:
    return s.scalar(select(Team).where(func.lower(Team.name) == name.lower()))


def teams(s: Session) -> list[Team]:
    stmt = select(Team).options(selectinload(Team.memberships)).order_by(Team.name)
    return list(s.scalars(stmt))


def add_team(s: Session, name: str) -> Team:
    item = Team(name=name)
    s.add(item)
    s.flush()
    return item


def delete_team(s: Session, item: Team) -> None:
    s.delete(item)


def memberships(s: Session, user_id: int) -> list[Membership]:
    return list(s.scalars(select(Membership).where(Membership.user_id == user_id)))


def members(s: Session, team_id: int) -> list[Membership]:
    stmt = (
        select(Membership)
        .where(Membership.team_id == team_id)
        .options(selectinload(Membership.user))
    )
    return list(s.scalars(stmt))


def membership(s: Session, user_id: int, team_id: int) -> Membership | None:
    stmt = select(Membership).where(Membership.user_id == user_id, Membership.team_id == team_id)
    return s.scalar(stmt)


def set_member(s: Session, user_id: int, team_id: int, role: TeamRole) -> Membership:
    item = membership(s, user_id, team_id)
    if item is None:
        item = Membership(user_id=user_id, team_id=team_id, role=role)
        s.add(item)
    item.role = role
    s.flush()
    return item


def remove_member(s: Session, user_id: int, team_id: int) -> None:
    item = membership(s, user_id, team_id)
    if item is not None:
        s.delete(item)
