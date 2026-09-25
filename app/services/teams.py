"""Teams and memberships. Admins create teams; owners manage members."""

from dataclasses import dataclass

from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Space, Team
from app.enums import SpaceKind, TeamRole
from app.repos import content, users
from app.services import audit
from app.services.access import AccessDenied, Principal


class TeamError(ValueError):
    pass


@dataclass
class MemberView:
    user_id: int
    name: str
    email: str
    role: TeamRole


@dataclass
class TeamView:
    id: int
    name: str
    space_id: int
    my_role: TeamRole | None
    members: list[MemberView]


def create_in(s: Session, name: str) -> Team:
    """Team plus its space. Caller owns the transaction."""
    name = name.strip()
    if not name:
        raise TeamError("team.name_missing")
    if users.team_by_name(s, name):
        raise TeamError("team.name_taken")

    team = users.add_team(s, name)
    content.add(s, Space(kind=SpaceKind.TEAM, name=name, team_id=team.id))
    return team


def create(who: Principal, name: str, ip: str = "") -> int:
    if not who.is_admin:
        raise AccessDenied("team.create")

    with session_scope() as s:
        team = create_in(s, name)
        audit.log(s, who.user_id, "team.created", target=team.name, ip=ip)
        return team.id


def overview(who: Principal) -> list[TeamView]:
    with session_scope() as s:
        result = []
        for team in users.teams(s):
            mine = who.teams.get(team.id)
            if mine is None and not who.is_admin:
                continue

            space = content.team_space(s, team.id)
            members = [
                MemberView(m.user_id, m.user.name, m.user.email, TeamRole(m.role))
                for m in users.members(s, team.id)
            ]
            result.append(TeamView(team.id, team.name, space.id, mine, members))
        return result


def _may_manage(who: Principal, team_id: int) -> None:
    if who.is_admin or who.teams.get(team_id) == TeamRole.OWNER:
        return

    raise AccessDenied("team.manage")


def set_member(who: Principal, team_id: int, user_id: int, role: TeamRole, ip: str = "") -> None:
    _may_manage(who, team_id)
    with session_scope() as s:
        if users.team(s, team_id) is None or users.get(s, user_id) is None:
            raise TeamError("team.not_found")

        users.set_member(s, user_id, team_id, role)
        audit.log(s, who.user_id, "team.member_set", target=str(team_id), ip=ip,
                  member=user_id, role=role.value)


def remove_member(who: Principal, team_id: int, user_id: int, ip: str = "") -> None:
    _may_manage(who, team_id)
    with session_scope() as s:
        users.remove_member(s, user_id, team_id)
        audit.log(s, who.user_id, "team.member_removed", target=str(team_id), ip=ip,
                  member=user_id)


def rename(who: Principal, team_id: int, name: str) -> None:
    _may_manage(who, team_id)
    with session_scope() as s:
        team = users.team(s, team_id)
        if team is None:
            raise TeamError("team.not_found")
        team.name = name.strip() or team.name
        content.team_space(s, team_id).name = team.name


def delete(who: Principal, team_id: int, ip: str = "") -> None:
    if not who.is_admin:
        raise AccessDenied("team.delete")

    with session_scope() as s:
        team = users.team(s, team_id)
        if team is None:
            return
        space = content.team_space(s, team_id)
        if space:
            content.remove(s, space)
        audit.log(s, who.user_id, "team.deleted", target=team.name, ip=ip)
        users.delete_team(s, team)
