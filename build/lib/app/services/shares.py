"""Explicit grants: "who has access?" dialog for boards, widgets, connections, themes."""

from dataclasses import dataclass

from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Share
from app.enums import CredentialMode, GranteeKind, ResourceKind, Right, ServiceType, SpaceKind
from app.repos import content, misc, users
from app.services import access, audit
from app.services.access import Principal
from app.services.util import NotFound

SHAREABLE = (Right.VIEW, Right.USE, Right.EDIT, Right.MANAGE)


class ShareError(ValueError):
    pass


@dataclass
class ShareView:
    id: int
    grantee_kind: GranteeKind
    grantee_id: int
    grantee_name: str
    right: Right


@dataclass
class Audience:
    """Who gets access through the space itself (for the dialog)."""

    space_name: str
    space_kind: SpaceKind
    members: list[tuple[str, str]]


@dataclass
class ShareInfo:
    title: str
    kind: ResourceKind
    resource_id: int
    shares: list[ShareView]
    audience: Audience
    warn_shared_data: bool
    users: list[tuple[int, str]]
    teams: list[tuple[int, str]]


def _resource(s: Session, kind: ResourceKind, resource_id: int):
    loader = {
        ResourceKind.BOARD: content.board,
        ResourceKind.WIDGET: content.widget,
        ResourceKind.CONNECTION: content.connection,
        ResourceKind.THEME: misc.theme,
    }[kind]
    item = loader(s, resource_id)
    if item is None:
        raise NotFound(kind.value)
    return item


def _need_manage(s: Session, who: Principal, kind: ResourceKind, item) -> None:
    space = access.space_of(s, who, item.space_id)
    access.need(access.right(who, kind, item.id, space, getattr(item, "min_team_role", None)),
                Right.MANAGE)


def info(who: Principal, kind: ResourceKind, resource_id: int) -> ShareInfo:
    with session_scope() as s:
        item = _resource(s, kind, resource_id)
        _need_manage(s, who, kind, item)
        names = {u.id: u.name for u in users.all_users(s)}
        team_names = {t.id: t.name for t in users.teams(s)}

        shares = [
            ShareView(
                sh.id,
                GranteeKind(sh.grantee_kind),
                sh.grantee_id,
                (names if sh.grantee_kind == GranteeKind.USER else team_names).get(sh.grantee_id, "?"),
                Right(sh.right),
            )
            for sh in misc.shares_for(s, kind, resource_id)
        ]

        space = content.space(s, item.space_id)
        members: list[tuple[str, str]] = []
        if space.kind == SpaceKind.TEAM:
            members = [(m.user.name, m.role) for m in users.members(s, space.team_id)]
        elif space.kind == SpaceKind.PERSONAL:
            members = [(names.get(space.owner_user_id, "?"), "owner")]

        warn = kind == ResourceKind.CONNECTION and item.credential_mode == CredentialMode.SHARED
        return ShareInfo(
            title=getattr(item, "title", None) or getattr(item, "name", ""),
            kind=kind,
            resource_id=resource_id,
            shares=shares,
            audience=Audience(space.name, SpaceKind(space.kind), members),
            warn_shared_data=warn,
            users=sorted(names.items(), key=lambda x: x[1]),
            teams=sorted(team_names.items(), key=lambda x: x[1]),
        )


def grant(who: Principal, kind: ResourceKind, resource_id: int, grantee_kind: GranteeKind,
          grantee_id: int, right: Right) -> None:
    if right not in SHAREABLE:
        raise ShareError("share.right")

    with session_scope() as s:
        item = _resource(s, kind, resource_id)
        _need_manage(s, who, kind, item)
        location = kind == ResourceKind.CONNECTION and ServiceType(item.service) == ServiceType.DAWARICH
        shared_data = location and item.credential_mode == CredentialMode.SHARED
        if shared_data and not misc.setting(s, "dawarich_shared").get("allowed"):
            raise ShareError("connection.location_personal")

        exists = [sh for sh in misc.shares_for(s, kind, resource_id)
                  if sh.grantee_kind == grantee_kind and sh.grantee_id == grantee_id]
        if exists:
            exists[0].right = int(right)
        else:
            misc.add(s, Share(resource_kind=kind, resource_id=resource_id, grantee_kind=grantee_kind,
                              grantee_id=grantee_id, right=int(right), created_by=who.user_id))
        audit.log(s, who.user_id, "share.granted", target=f"{kind.value}:{resource_id}",
                  grantee=f"{grantee_kind.value}:{grantee_id}", right=right.name)


def revoke(who: Principal, share_id: int) -> None:
    with session_scope() as s:
        share = misc.share(s, share_id)
        if share is None:
            return
        item = _resource(s, ResourceKind(share.resource_kind), share.resource_id)
        _need_manage(s, who, ResourceKind(share.resource_kind), item)
        audit.log(s, who.user_id, "share.revoked",
                  target=f"{share.resource_kind}:{share.resource_id}")
        misc.remove(s, share)
