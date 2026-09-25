"""Spaces and their content: connections, widgets, boards, overlays, revisions."""

from sqlalchemy import func, select
from sqlalchemy.orm import Session, selectinload

from app.db.models import (
    Board,
    Connection,
    Overlay,
    Placement,
    Revision,
    Section,
    Space,
    UserCredential,
    Widget,
)
from app.enums import RevisionKind, SpaceKind

REVISIONS_KEPT = 50


def add(s: Session, item):
    s.add(item)
    s.flush()
    return item


def remove(s: Session, item) -> None:
    s.delete(item)
    s.flush()


# ── Spaces ──


def space(s: Session, space_id: int) -> Space | None:
    return s.get(Space, space_id)


def personal_space(s: Session, user_id: int) -> Space | None:
    return s.scalar(select(Space).where(Space.owner_user_id == user_id))


def team_space(s: Session, team_id: int) -> Space | None:
    return s.scalar(select(Space).where(Space.team_id == team_id))


def instance_space(s: Session) -> Space | None:
    return s.scalar(select(Space).where(Space.kind == SpaceKind.INSTANCE))


def spaces(s: Session, ids: list[int]) -> list[Space]:
    if not ids:
        return []

    return list(s.scalars(select(Space).where(Space.id.in_(ids))))


def all_spaces(s: Session) -> list[Space]:
    return list(s.scalars(select(Space)))


# ── Connections ──


def connection(s: Session, conn_id: int) -> Connection | None:
    return s.get(Connection, conn_id)


def connections(s: Session, space_ids: list[int]) -> list[Connection]:
    stmt = select(Connection).where(Connection.space_id.in_(space_ids)).order_by(Connection.name)
    return list(s.scalars(stmt))


def all_connections(s: Session) -> list[Connection]:
    return list(s.scalars(select(Connection)))


def connection_by_key(s: Session, space_id: int, key: str) -> Connection | None:
    stmt = select(Connection).where(Connection.space_id == space_id, Connection.key == key)
    return s.scalar(stmt)


def credential(s: Session, conn_id: int, user_id: int) -> UserCredential | None:
    stmt = select(UserCredential).where(
        UserCredential.connection_id == conn_id, UserCredential.user_id == user_id
    )
    return s.scalar(stmt)


def credentials(s: Session, conn_id: int) -> list[UserCredential]:
    stmt = select(UserCredential).where(UserCredential.connection_id == conn_id)
    return list(s.scalars(stmt))


# ── Widgets ──


def widget(s: Session, widget_id: int) -> Widget | None:
    return s.get(Widget, widget_id)


def widgets(s: Session, space_ids: list[int]) -> list[Widget]:
    stmt = select(Widget).where(Widget.space_id.in_(space_ids)).order_by(Widget.title)
    return list(s.scalars(stmt))


def widget_by_key(s: Session, space_id: int, key: str) -> Widget | None:
    return s.scalar(select(Widget).where(Widget.space_id == space_id, Widget.key == key))


def widget_uses(s: Session, widget_id: int) -> int:
    stmt = select(func.count(Placement.id)).where(Placement.widget_id == widget_id)
    return s.scalar(stmt) or 0


def widgets_on_connection(s: Session, conn_id: int) -> list[Widget]:
    return list(s.scalars(select(Widget).where(Widget.connection_id == conn_id)))


# ── Boards ──


def _board_query():
    return select(Board).options(
        selectinload(Board.sections)
        .selectinload(Section.placements)
        .selectinload(Placement.widget)
    )


def board(s: Session, board_id: int) -> Board | None:
    return s.scalar(_board_query().where(Board.id == board_id))


def board_by_slug(s: Session, space_id: int, slug: str) -> Board | None:
    return s.scalar(_board_query().where(Board.space_id == space_id, Board.slug == slug))


def boards(s: Session, space_ids: list[int]) -> list[Board]:
    stmt = select(Board).where(Board.space_id.in_(space_ids)).order_by(Board.position, Board.id)
    return list(s.scalars(stmt))


def section(s: Session, section_id: int) -> Section | None:
    return s.get(Section, section_id)


def placement(s: Session, placement_id: int) -> Placement | None:
    stmt = (
        select(Placement)
        .where(Placement.id == placement_id)
        .options(selectinload(Placement.widget), selectinload(Placement.section))
    )
    return s.scalar(stmt)


def overlay(s: Session, user_id: int, board_id: int) -> Overlay | None:
    stmt = select(Overlay).where(Overlay.user_id == user_id, Overlay.board_id == board_id)
    return s.scalar(stmt)


# ── Revisions ──


def revisions(s: Session, kind: RevisionKind, entity_id: int) -> list[Revision]:
    stmt = (
        select(Revision)
        .where(Revision.kind == kind, Revision.entity_id == entity_id)
        .order_by(Revision.id.desc())
    )
    return list(s.scalars(stmt))


def revision(s: Session, rev_id: int) -> Revision | None:
    return s.get(Revision, rev_id)


def prune_revisions(s: Session, kind: RevisionKind, entity_id: int) -> None:
    for old in revisions(s, kind, entity_id)[REVISIONS_KEPT:]:
        s.delete(old)
