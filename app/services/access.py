"""Who may do what.

    right = max( space role right  (minus team restriction),
                 explicit shares to the user or one of his teams )

    space        owner/admin   editor   viewer/member   other
    personal     MANAGE        –        –               NONE
    team         MANAGE        EDIT     USE             NONE
    instance     MANAGE(admin) –        USE             USE

Instance admins manage accounts, never the content of personal spaces.
"""

from dataclasses import dataclass, field

from sqlalchemy.orm import Session

from app.enums import InstanceRole, Locale, ResourceKind, Right, SpaceKind, TeamRole
from app.repos import content, misc, users

TEAM_RANK = {TeamRole.VIEWER: 1, TeamRole.EDITOR: 2, TeamRole.OWNER: 3}
TEAM_RIGHT = {TeamRole.OWNER: Right.MANAGE, TeamRole.EDITOR: Right.EDIT, TeamRole.VIEWER: Right.USE}


class AccessDenied(Exception):
    pass


@dataclass(frozen=True)
class SpaceRef:
    id: int
    kind: SpaceKind
    owner_user_id: int | None
    team_id: int | None
    name: str


@dataclass
class Principal:
    """The acting user with everything needed for access decisions."""

    user_id: int
    name: str
    email: str
    role: InstanceRole
    locale: Locale
    teams: dict[int, TeamRole] = field(default_factory=dict)
    grants: dict[tuple[ResourceKind, int], Right] = field(default_factory=dict)
    spaces: dict[int, SpaceRef] = field(default_factory=dict)
    session_id: int | None = None
    token_boards: list[int] | None = None

    @property
    def is_admin(self) -> bool:
        return self.role == InstanceRole.ADMIN


def principal(s: Session, user_id: int) -> Principal | None:
    user = users.get(s, user_id)
    if user is None or not user.is_active:
        return None

    teams = {m.team_id: TeamRole(m.role) for m in users.memberships(s, user_id)}
    who = Principal(
        user_id=user.id,
        name=user.name,
        email=user.email,
        role=InstanceRole(user.role),
        locale=Locale(user.locale),
        teams=teams,
    )

    for share in misc.shares_to(s, user_id, list(teams)):
        key = (ResourceKind(share.resource_kind), share.resource_id)
        who.grants[key] = max(who.grants.get(key, Right.NONE), Right(share.right))

    for space in _reachable_spaces(s, who):
        who.spaces[space.id] = SpaceRef(
            space.id, SpaceKind(space.kind), space.owner_user_id, space.team_id, space.name
        )

    return who


def _reachable_spaces(s: Session, who: Principal) -> list:
    found = []
    mine = content.personal_space(s, who.user_id)
    if mine:
        found.append(mine)

    for team_id in who.teams:
        space = content.team_space(s, team_id)
        if space:
            found.append(space)

    shared = content.instance_space(s)
    if shared:
        found.append(shared)

    return found


def space_right(who: Principal, space: SpaceRef | None) -> Right:
    if space is None:
        return Right.NONE

    if space.kind == SpaceKind.PERSONAL:
        return Right.MANAGE if space.owner_user_id == who.user_id else Right.NONE

    if space.kind == SpaceKind.INSTANCE:
        return Right.MANAGE if who.is_admin else Right.USE

    role = who.teams.get(space.team_id or 0)
    return TEAM_RIGHT[role] if role else Right.NONE


def right(
    who: Principal,
    kind: ResourceKind,
    resource_id: int,
    space: SpaceRef | None,
    min_role: TeamRole | None = None,
) -> Right:
    base = space_right(who, space)
    if min_role and space and space.kind == SpaceKind.TEAM and base < Right.MANAGE:
        role = who.teams.get(space.team_id or 0)
        if role is None or TEAM_RANK[role] < TEAM_RANK[TeamRole(min_role)]:
            base = Right.NONE

    granted = who.grants.get((kind, resource_id), Right.NONE)
    return max(base, granted)


def space_of(s: Session, who: Principal, space_id: int) -> SpaceRef | None:
    """Space reference, also for spaces reached only through a share."""
    known = who.spaces.get(space_id)
    if known:
        return known

    space = content.space(s, space_id)
    if space is None:
        return None

    return SpaceRef(space.id, SpaceKind(space.kind), space.owner_user_id, space.team_id, space.name)


def need(granted: Right, required: Right) -> None:
    if granted < required:
        raise AccessDenied(required.name)


def editable_spaces(who: Principal) -> list[SpaceRef]:
    return [sp for sp in who.spaces.values() if space_right(who, sp) >= Right.EDIT]


def personal(who: Principal) -> SpaceRef | None:
    for space in who.spaces.values():
        if space.kind == SpaceKind.PERSONAL:
            return space

    return None
