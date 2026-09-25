"""Space settings (goals, tax values, hint handling, team theme)."""

from dataclasses import dataclass

from app.db.base import session_scope
from app.enums import Right, SpaceKind
from app.repos import content
from app.rules import base as rules
from app.services import access, audit
from app.services.access import Principal, SpaceRef
from app.services.util import NotFound


def settings(who: Principal, space_id: int) -> dict:
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.VIEW)
        raw = content.space(s, space_id)
        return dict(raw.settings or {})


def update_settings(who: Principal, space_id: int, changes: dict) -> None:
    """Team settings need the owner role; personal ones the owner."""
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        if space is None:
            raise NotFound("space")
        need = Right.MANAGE if space.kind != SpaceKind.PERSONAL else Right.EDIT
        access.need(access.space_right(who, space), need)
        raw = content.space(s, space_id)
        raw.settings = {**(raw.settings or {}), **changes}
        raw.version += 1
        audit.log(s, who.user_id, "space.settings", target=raw.name, keys=sorted(changes))


def raw_settings(space_id: int) -> dict:
    """For jobs (rules): no access check."""
    with session_scope() as s:
        raw = content.space(s, space_id)
        return dict(raw.settings or {}) if raw else {}


def mine(who: Principal) -> SpaceRef | None:
    return access.personal(who)


@dataclass
class RuleView:
    id: str
    scope: str
    values: dict


def rule_views(settings: dict) -> list[RuleView]:
    """All rules with their effective parameters for the settings form."""
    return [RuleView(spec.id, spec.scope, rules.config(spec, settings)) for spec in rules.all_rules()]


def rule_defaults() -> dict[str, dict]:
    return {spec.id: spec.defaults for spec in rules.all_rules()}
