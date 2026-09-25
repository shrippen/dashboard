"""Boards: what a user sees, and how editors change layouts.

    Board ─ Section ─ Placement → Widget          (shared structure)
                         ▲
    Overlay(user, board): order, hidden, collapsed, size   (personal layer)

Editors (EDIT on the board) change the board itself. Everybody else
dragging tiles around changes only his overlay.
"""

from dataclasses import dataclass, field
from enum import StrEnum

from pydantic import BaseModel
from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Board, Overlay, Placement, Revision, Section, Widget
from app.enums import (
    ResourceKind,
    RevisionKind,
    Right,
    SortOrder,
    SpaceKind,
    TeamRole,
    TileSize,
)
from app.repos import content, misc
from app.services import access, porting, widgets
from app.services.access import AccessDenied, Principal, SpaceRef
from app.services.data import Freshness
from app.services.util import Conflict, NotFound, slug, unique
from app.widgets import base as types

AREAS = ("main", "side")
START_SLUG = "start"


class LayoutTarget(StrEnum):
    BOARD = "board"
    OVERLAY = "overlay"


@dataclass
class Tile:
    placement_id: int
    widget_id: int
    type: str
    title: str
    template: str
    inline: bool
    refresh_s: int | None
    config: BaseModel
    hidden: bool
    fragment: widgets.Fragment | None = None


@dataclass
class SectionView:
    id: int
    title: str
    cols: int | None
    size: TileSize
    sort: SortOrder
    collapsed: bool
    area: str
    tiles: list[Tile] = field(default_factory=list)


@dataclass
class BoardView:
    id: int
    slug: str
    name: str
    space: SpaceRef
    version: int
    theme_id: int | None
    can_edit: bool
    has_overlay: bool
    sections: list[SectionView] = field(default_factory=list)


@dataclass
class BoardRef:
    id: int
    slug: str
    name: str
    space: SpaceRef
    can_edit: bool


# ── Rights ──


def _board_right(who: Principal, s: Session, board: Board) -> Right:
    if who.token_boards is not None and board.id not in who.token_boards:
        return Right.NONE

    space = access.space_of(s, who, board.space_id)
    return access.right(who, ResourceKind.BOARD, board.id, space, board.min_team_role)


def _widget_right(who: Principal, s: Session, widget: Widget) -> Right:
    space = access.space_of(s, who, widget.space_id)
    return access.right(who, ResourceKind.WIDGET, widget.id, space, widget.min_team_role)


def _seen_right(who: Principal, s: Session, widget: Widget, board: Board) -> Right:
    """Viewing right on a placed widget.

    A board shared with somebody shows its own space's widgets to him, e.g. Alex
    shares his board: Bea sees Alex's tiles on it, but no widgets of other spaces.
    """
    granted = _widget_right(who, s, widget)
    if widget.space_id == board.space_id and widget.min_team_role is None:
        granted = max(granted, min(_board_right(who, s, board), Right.VIEW))
    return granted


def _load(s: Session, who: Principal, board_id: int, required: Right) -> Board:
    board = content.board(s, board_id)
    if board is None:
        raise NotFound("board")
    access.need(_board_right(who, s, board), required)
    return board


# ── Listing ──


def visible(who: Principal) -> list[BoardRef]:
    with session_scope() as s:
        ids = list(who.spaces)
        found = content.boards(s, ids)
        shared = [rid for (kind, rid) in who.grants if kind == ResourceKind.BOARD]
        found += [b for b in (content.board(s, rid) for rid in shared) if b and b.space_id not in ids]

        result = []
        for board in found:
            granted = _board_right(who, s, board)
            if granted < Right.VIEW or board.is_template:
                continue
            space = access.space_of(s, who, board.space_id)
            result.append(BoardRef(board.id, board.slug, board.name, space, granted >= Right.EDIT))

        order = {SpaceKind.PERSONAL: 0, SpaceKind.TEAM: 1, SpaceKind.INSTANCE: 2}
        result.sort(key=lambda b: order[b.space.kind])
        return result


def start_board(who: Principal, preferred: int | None) -> int:
    """User's start board; creates an empty personal one on first visit."""
    listed = visible(who)
    ids = {b.id for b in listed}
    if preferred in ids:
        return preferred
    if listed:
        return listed[0].id

    personal = access.personal(who)
    if personal is None:
        raise NotFound("space")
    return create(who, personal.id, "Start")


# ── View ──


def view(who: Principal, board_id: int) -> BoardView:
    with session_scope() as s:
        board = _load(s, who, board_id, Right.VIEW)
        granted = _board_right(who, s, board)
        overlay = content.overlay(s, who.user_id, board.id)
        layer = dict(overlay.data) if overlay else {}
        result = BoardView(
            id=board.id,
            slug=board.slug,
            name=board.name,
            space=access.space_of(s, who, board.space_id),
            version=board.version,
            theme_id=board.theme_id,
            can_edit=granted >= Right.EDIT,
            has_overlay=bool(layer),
        )
        for section in board.sections:
            result.sections.append(_section(s, who, section, layer))

    return result


def _section(s: Session, who: Principal, section: Section, layer: dict) -> SectionView:
    key = str(section.id)
    view_ = SectionView(
        id=section.id,
        title=section.title,
        cols=section.cols,
        size=TileSize(layer.get("size", {}).get(key, section.size)),
        sort=SortOrder(section.sort),
        collapsed=layer.get("collapsed", {}).get(key, section.collapsed),
        area=section.area if section.area in AREAS else AREAS[0],
    )
    hidden = set(layer.get("hidden", []))
    order = layer.get("order", {}).get(key)
    placements = list(section.placements)
    if order:
        rank = {pid: i for i, pid in enumerate(order)}
        placements.sort(key=lambda p: rank.get(p.id, len(rank) + p.position))

    for placement in placements:
        widget = placement.widget
        kind = types.get(widget.type)
        if kind is None or _seen_right(who, s, widget, section.board) < Right.VIEW:
            continue
        view_.tiles.append(
            Tile(
                placement_id=placement.id,
                widget_id=widget.id,
                type=widget.type,
                title=widget.title,
                template=kind.template,
                inline=kind.inline,
                refresh_s=kind.refresh_s,
                config=kind.schema.model_validate(widget.config or {}),
                hidden=placement.id in hidden,
                fragment=widgets.peek(who, widget) if kind.inline else None,
            )
        )

    if view_.sort == SortOrder.ALPHABETICAL:
        view_.tiles.sort(key=lambda t: t.title.lower())
    return view_


def placed_widget(who: Principal, placement_id: int) -> Widget:
    with session_scope() as s:
        placement = content.placement(s, placement_id)
        if placement is None:
            raise NotFound("placement")
        board = _load(s, who, placement.section.board_id, Right.VIEW)
        widget = placement.widget
        access.need(_seen_right(who, s, widget, board), Right.VIEW)
        return widget


def fragment(
    who: Principal, placement_id: int, fresh: Freshness = Freshness.CACHED
) -> widgets.Fragment:
    return widgets.load(who, placed_widget(who, placement_id), fresh)


# ── Board changes (EDIT) ──


def create(who: Principal, space_id: int, name: str) -> int:
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)
        taken = {b.slug for b in content.boards(s, [space_id])}
        board = content.add(
            s,
            Board(
                space_id=space_id,
                slug=unique(slug(name, START_SLUG), taken),
                name=name.strip() or "Board",
                position=len(taken),
            ),
        )
        content.add(s, Section(board_id=board.id, title="", position=0))
        _revision(s, who, board)
        return board.id


def rename(who: Principal, board_id: int, version: int, name: str,
           theme_id: int | None, min_role: TeamRole | None) -> None:
    with session_scope() as s:
        board = _load(s, who, board_id, Right.EDIT)
        _bump(board, version)
        board.name = name.strip() or board.name
        board.theme_id = theme_id
        board.min_team_role = min_role
        _revision(s, who, board)


def delete(who: Principal, board_id: int) -> None:
    with session_scope() as s:
        board = _load(s, who, board_id, Right.MANAGE)
        misc.drop_shares(s, ResourceKind.BOARD, board.id)
        content.remove(s, board)


def _bump(board: Board, version: int) -> None:
    if board.version != version:
        raise Conflict("board")
    board.version += 1


def add_section(who: Principal, board_id: int, version: int, title: str) -> int:
    with session_scope() as s:
        board = _load(s, who, board_id, Right.EDIT)
        _bump(board, version)
        section = content.add(
            s, Section(board_id=board.id, title=title.strip(), position=len(board.sections))
        )
        _revision(s, who, board)
        return section.id


def edit_section(who: Principal, section_id: int, version: int, **changes) -> None:
    allowed = {"title", "cols", "size", "sort", "collapsed", "area"}
    with session_scope() as s:
        section = content.section(s, section_id)
        if section is None:
            raise NotFound("section")
        board = _load(s, who, section.board_id, Right.EDIT)
        _bump(board, version)
        for key, value in changes.items():
            if key in allowed:
                setattr(section, key, value)
        _revision(s, who, board)


def delete_section(who: Principal, section_id: int, version: int) -> None:
    with session_scope() as s:
        section = content.section(s, section_id)
        if section is None:
            return
        board = _load(s, who, section.board_id, Right.EDIT)
        _bump(board, version)
        content.remove(s, section)
        _revision(s, who, board)


def place(who: Principal, section_id: int, widget_id: int, version: int) -> int:
    """Put a library widget on a board (needs USE on the widget)."""
    with session_scope() as s:
        section = content.section(s, section_id)
        if section is None:
            raise NotFound("section")
        board = _load(s, who, section.board_id, Right.EDIT)
        widget = content.widget(s, widget_id)
        if widget is None:
            raise NotFound("widget")
        access.need(_widget_right(who, s, widget), Right.USE)
        _bump(board, version)
        placement = content.add(
            s,
            Placement(section_id=section.id, widget_id=widget.id, position=len(section.placements)),
        )
        _revision(s, who, board)
        return placement.id


def unplace(who: Principal, placement_id: int, version: int) -> None:
    with session_scope() as s:
        placement = content.placement(s, placement_id)
        if placement is None:
            return
        board = _load(s, who, placement.section.board_id, Right.EDIT)
        _bump(board, version)
        content.remove(s, placement)
        _revision(s, who, board)


def arrange(who: Principal, board_id: int, version: int, layout: dict[int, list[int]]) -> LayoutTarget:
    """Drag and drop result {section_id: [placement ids]}.

    Editors reorder the board (tiles may move between sections); others
    store the order in their overlay (only within a section).
    """
    with session_scope() as s:
        board = _load(s, who, board_id, Right.VIEW)
        known = {p.id: p for sec in board.sections for p in sec.placements}
        sections = {sec.id for sec in board.sections}
        if any(sid not in sections for sid in layout) or any(
            pid not in known for pids in layout.values() for pid in pids
        ):
            raise NotFound("layout")

        if _board_right(who, s, board) >= Right.EDIT:
            _bump(board, version)
            for section_id, pids in layout.items():
                for index, pid in enumerate(pids):
                    known[pid].section_id = section_id
                    known[pid].position = index
            _revision(s, who, board)
            return LayoutTarget.BOARD

        layer = _overlay(s, who, board.id)
        data = dict(layer.data or {})
        order = dict(data.get("order", {}))
        for section_id, pids in layout.items():
            order[str(section_id)] = [pid for pid in pids if known[pid].section_id == section_id]
        data["order"] = order
        layer.data = data
        return LayoutTarget.OVERLAY


# ── Personal overlay ──


def _overlay(s: Session, who: Principal, board_id: int) -> Overlay:
    found = content.overlay(s, who.user_id, board_id)
    if found is None:
        found = content.add(s, Overlay(user_id=who.user_id, board_id=board_id, data={}))
    return found


class Fold(StrEnum):
    OPEN = "open"
    CLOSED = "closed"


class Visibility(StrEnum):
    SHOWN = "shown"
    HIDDEN = "hidden"


def _set_layer(who: Principal, board_id: int, key: str, section_or_pid: int, value) -> None:
    with session_scope() as s:
        _load(s, who, board_id, Right.VIEW)
        layer = _overlay(s, who, board_id)
        data = dict(layer.data or {})
        if key == "hidden":
            hidden = set(data.get("hidden", []))
            if value:
                hidden.add(section_or_pid)
            else:
                hidden.discard(section_or_pid)
            data["hidden"] = sorted(hidden)
        else:
            entry = dict(data.get(key, {}))
            entry[str(section_or_pid)] = value
            data[key] = entry
        layer.data = data


def fold(who: Principal, board_id: int, section_id: int, state: Fold) -> None:
    _set_layer(who, board_id, "collapsed", section_id, state == Fold.CLOSED)


def show(who: Principal, board_id: int, placement_id: int, state: Visibility) -> None:
    _set_layer(who, board_id, "hidden", placement_id, state == Visibility.HIDDEN)


def resize(who: Principal, board_id: int, section_id: int, size: TileSize) -> None:
    _set_layer(who, board_id, "size", section_id, size.value)


def reset_overlay(who: Principal, board_id: int) -> None:
    with session_scope() as s:
        found = content.overlay(s, who.user_id, board_id)
        if found:
            content.remove(s, found)


# ── Revisions ──


def _revision(s: Session, who: Principal, board: Board) -> None:
    s.flush()
    s.refresh(board)
    spaces = {sp.id: sp for sp in content.all_spaces(s)}
    content.add(
        s,
        Revision(
            kind=RevisionKind.BOARD,
            entity_id=board.id,
            space_id=board.space_id,
            user_id=who.user_id,
            version=board.version,
            data=porting.board_dict(board, spaces),
        ),
    )
    content.prune_revisions(s, RevisionKind.BOARD, board.id)


@dataclass
class RevisionView:
    id: int
    version: int
    at: object
    user_id: int | None
    data: dict


def history(who: Principal, board_id: int) -> list[RevisionView]:
    with session_scope() as s:
        _load(s, who, board_id, Right.EDIT)
        return [
            RevisionView(r.id, r.version, r.created_at, r.user_id, r.data)
            for r in content.revisions(s, RevisionKind.BOARD, board_id)
        ]


def restore(who: Principal, board_id: int, revision_id: int) -> None:
    """Rebuild sections and placements from a stored revision."""
    with session_scope() as s:
        board = _load(s, who, board_id, Right.EDIT)
        rev = content.revision(s, revision_id)
        if rev is None or rev.entity_id != board.id or rev.kind != RevisionKind.BOARD:
            raise NotFound("revision")

        for section in list(board.sections):
            content.remove(s, section)
        board.name = rev.data.get("name", board.name)
        board.version += 1
        for index, raw in enumerate(rev.data.get("sections", [])):
            section = content.add(
                s,
                Section(
                    board_id=board.id,
                    title=raw.get("title", ""),
                    position=index,
                    cols=raw.get("cols"),
                    size=TileSize(raw.get("size", TileSize.MEDIUM)),
                    sort=SortOrder(raw.get("sort", SortOrder.MANUAL)),
                    collapsed=raw.get("collapsed", False),
                    area=raw.get("area", AREAS[0]),
                ),
            )
            for pos, ref in enumerate(raw.get("widgets", [])):
                widget_id = porting.resolve(s, who, board.space_id, str(ref))
                if widget_id is None:
                    continue
                content.add(s, Placement(section_id=section.id, widget_id=widget_id, position=pos))
        _revision(s, who, board)


def ensure_editable(who: Principal, board_id: int) -> None:
    with session_scope() as s:
        board = content.board(s, board_id)
        if board is None:
            raise NotFound("board")
        if _board_right(who, s, board) < Right.EDIT:
            raise AccessDenied("board")
