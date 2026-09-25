"""Widget library: create, change, delete, and load data for display.

    placement ──► widget ──► type.queries(config) ──► data.get(source, conn, user)
        │            │
     board right   widget right (view)            personal credentials → per user
"""

from dataclasses import dataclass, field

from pydantic import BaseModel, ValidationError
from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Connection, Revision, Widget
from app.enums import ResourceKind, RevisionKind, Right, ServiceType, TeamRole
from app.repos import content, misc
from app.services import access, data, hints, porting
from app.services.access import AccessDenied, Principal, SpaceRef
from app.services.data import Freshness
from app.services.util import Conflict, NotFound, slug, unique
from app.widgets import base as types
from app.widgets.base import ConnUse


class WidgetError(ValueError):
    pass


@dataclass
class WidgetRef:
    id: int
    key: str
    type: str
    title: str
    space: SpaceRef
    connection_id: int | None
    uses: int
    can_edit: bool


@dataclass
class Slot:
    """Result of one query for the template."""

    data: dict | None = None
    error: str | None = None
    ok_at: object = None
    missing_credential: str | None = None

    @property
    def ok(self) -> bool:
        return self.error is None and self.data is not None


@dataclass
class Fragment:
    widget_id: int
    type: str
    title: str
    config: BaseModel
    slots: dict[str, Slot] = field(default_factory=dict)
    hint_count: int = 0
    hint_level: int = 0


# ── Library ──


def _widget_right(who: Principal, s: Session, widget: Widget) -> Right:
    space = access.space_of(s, who, widget.space_id)
    return access.right(who, ResourceKind.WIDGET, widget.id, space, widget.min_team_role)


def library(who: Principal) -> list[WidgetRef]:
    """Widgets the user may place: own spaces plus shared ones."""
    with session_scope() as s:
        ids = set(who.spaces)
        shared = [rid for (kind, rid) in who.grants if kind == ResourceKind.WIDGET]
        found = content.widgets(s, list(ids))
        found += [w for w in (content.widget(s, rid) for rid in shared) if w and w.space_id not in ids]

        result = []
        for widget in found:
            granted = _widget_right(who, s, widget)
            if granted < Right.USE:
                continue
            result.append(
                WidgetRef(
                    widget.id,
                    widget.key,
                    widget.type,
                    widget.title,
                    access.space_of(s, who, widget.space_id),
                    widget.connection_id,
                    content.widget_uses(s, widget.id),
                    granted >= Right.EDIT,
                )
            )
        return result


def validate(kind_key: str, config: dict) -> BaseModel:
    kind = types.get(kind_key)
    if kind is None:
        raise WidgetError("widget.unknown_type")

    try:
        return kind.schema.model_validate(config or {})
    except ValidationError as exc:
        raise WidgetError(_first_error(exc)) from exc


def _first_error(exc: ValidationError) -> str:
    err = exc.errors()[0]
    where = ".".join(str(p) for p in err.get("loc", ()))
    return f"{where}: {err.get('msg', 'invalid')}"


def _check_connection(s: Session, who: Principal, conn_id: int | None, kind_key: str) -> None:
    kind = types.get(kind_key)
    if conn_id is None:
        if kind.service is not None:
            raise WidgetError("widget.connection_required")
        return

    conn = content.connection(s, conn_id)
    if conn is None:
        raise WidgetError("widget.connection_missing")

    space = access.space_of(s, who, conn.space_id)
    access.need(access.right(who, ResourceKind.CONNECTION, conn.id, space), Right.USE)
    if kind.service is not None and ServiceType(conn.service) != kind.service:
        raise WidgetError("widget.connection_type")


def create(
    who: Principal,
    space_id: int,
    kind_key: str,
    title: str,
    config: dict,
    conn_id: int | None = None,
    min_role: TeamRole | None = None,
) -> int:
    parsed = validate(kind_key, config)
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)
        _check_connection(s, who, conn_id, kind_key)

        taken = {w.key for w in content.widgets(s, [space_id])}
        widget = content.add(
            s,
            Widget(
                space_id=space_id,
                key=unique(slug(title or kind_key, kind_key), taken),
                type=kind_key,
                title=title.strip(),
                config=parsed.model_dump(mode="json"),
                connection_id=conn_id,
                min_team_role=min_role,
            ),
        )
        _revision(s, who, widget)
        return widget.id


def detail(who: Principal, widget_id: int) -> tuple[Widget, Right]:
    with session_scope() as s:
        widget = content.widget(s, widget_id)
        if widget is None:
            raise NotFound("widget")
        granted = _widget_right(who, s, widget)
        access.need(granted, Right.VIEW)
        return widget, granted


def update(
    who: Principal,
    widget_id: int,
    version: int,
    title: str,
    config: dict,
    conn_id: int | None,
    min_role: TeamRole | None,
) -> None:
    with session_scope() as s:
        widget = content.widget(s, widget_id)
        if widget is None:
            raise NotFound("widget")
        access.need(_widget_right(who, s, widget), Right.EDIT)
        if widget.version != version:
            raise Conflict("widget")

        parsed = validate(widget.type, config)
        _check_connection(s, who, conn_id, widget.type)
        widget.title = title.strip()
        widget.config = parsed.model_dump(mode="json")
        widget.connection_id = conn_id
        widget.min_team_role = min_role
        widget.version += 1
        _revision(s, who, widget)


def copy(who: Principal, widget_id: int, space_id: int) -> int:
    """Independent copy ("Als Kopie übernehmen")."""
    widget, _right = detail(who, widget_id)
    return create(who, space_id, widget.type, widget.title, widget.config, widget.connection_id)


def delete(who: Principal, widget_id: int) -> None:
    with session_scope() as s:
        widget = content.widget(s, widget_id)
        if widget is None:
            return
        access.need(_widget_right(who, s, widget), Right.MANAGE)
        misc.drop_shares(s, ResourceKind.WIDGET, widget.id)
        content.remove(s, widget)


def _revision(s: Session, who: Principal, widget: Widget) -> None:
    content.add(
        s,
        Revision(
            kind=RevisionKind.WIDGET,
            entity_id=widget.id,
            space_id=widget.space_id,
            user_id=who.user_id,
            version=widget.version,
            data=porting.widget_dict(widget, None),
        ),
    )
    content.prune_revisions(s, RevisionKind.WIDGET, widget.id)


# ── Display ──


def _info_connection(s: Session, who: Principal, widget: Widget, key: str) -> Connection | None:
    """Info lines name a connection by key: first the widget's space, then any reachable one."""
    found = content.connection_by_key(s, widget.space_id, key)
    if found:
        return found

    for space_id in who.spaces:
        found = content.connection_by_key(s, space_id, key)
        if found:
            return found

    return None


def load(who: Principal, widget: Widget, fresh: Freshness = Freshness.CACHED) -> Fragment:
    kind = types.get(widget.type)
    config = kind.schema.model_validate(widget.config or {})
    result = Fragment(widget.id, widget.type, widget.title, config)

    with session_scope() as s:
        conn = content.connection(s, widget.connection_id) if widget.connection_id else None
        info_key = getattr(getattr(config, "info", None), "connection", None)
        info_conn = _info_connection(s, who, widget, info_key) if info_key else None

    for query in kind.queries(config):
        target = conn if query.conn == ConnUse.WIDGET else info_conn
        if query.conn == ConnUse.NONE:
            target = None
        if query.conn != ConnUse.NONE and target is None:
            result.slots[query.name] = Slot(error="connection.missing")
            continue

        source = f"{target.service}.info" if query.conn == ConnUse.INFO else query.source
        result.slots[query.name] = _run(source, query.params, target, who.user_id, fresh)

    hint_conn = info_conn or conn
    if hint_conn is not None:
        result.hint_count, result.hint_level = hints.count_for(who, hint_conn.id)

    return result


def _run(source: str, params: dict, conn, user_id: int, fresh: Freshness) -> Slot:
    try:
        res = data.get(source, params, conn, user_id, fresh)
    except data.MissingCredential as exc:
        return Slot(missing_credential=str(exc))
    except KeyError:
        return Slot(error="source.unknown")

    return Slot(res.data, res.error, res.ok_at)


def peek(who: Principal, widget: Widget) -> Fragment:
    """Cached data only (no network) for inline tiles in the first render."""
    kind = types.get(widget.type)
    config = kind.schema.model_validate(widget.config or {})
    result = Fragment(widget.id, widget.type, widget.title, config)
    for query in kind.queries(config):
        if query.conn != ConnUse.NONE:
            continue
        res = data.peek(query.source, query.params, None, who.user_id)
        result.slots[query.name] = Slot(res.data, res.error, res.ok_at)
    return result


def ensure_visible(who: Principal, widget_id: int) -> None:
    with session_scope() as s:
        widget = content.widget(s, widget_id)
        if widget is None:
            raise NotFound("widget")
        if _widget_right(who, s, widget) < Right.VIEW:
            raise AccessDenied("widget")


def preview(who: Principal, space_id: int, kind_key: str, title: str, config: dict,
            conn_id: int | None) -> Fragment:
    """Render data for unsaved settings (editor preview). Nothing is stored."""
    parsed = validate(kind_key, config)
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)
        _check_connection(s, who, conn_id, kind_key)

    draft = Widget(id=0, space_id=space_id, key="preview", type=kind_key, title=title,
                   config=parsed.model_dump(mode="json"), connection_id=conn_id)
    return load(who, draft)


def schema_of(kind_key: str) -> type[BaseModel]:
    kind = types.get(kind_key)
    if kind is None:
        raise WidgetError("widget.unknown_type")
    return kind.schema


def type_list() -> list[types.WidgetType]:
    return types.all_types()
