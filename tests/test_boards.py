"""Board editing, overlays, optimistic locking, revisions, HTTP views."""

import pytest

from app.enums import TileSize
from app.services import access, boards, widgets
from app.services.boards import Fold, LayoutTarget
from app.services.util import Conflict
from tests.conftest import make_user, who

LINK = {"url": "https://example.org", "status": "off"}


def _board_with_links(uid, count=3):
    space = access.personal(who(uid)).id
    board = boards.create(who(uid), space, "Start")
    view = boards.view(who(uid), board)
    for i in range(count):
        wid = widgets.create(who(uid), space, "link", f"L{i}", LINK)
        view = boards.view(who(uid), board)
        boards.place(who(uid), view.sections[0].id, wid, view.version)
    return board


def test_arrange_by_editor_changes_board(app):
    uid = make_user("a@x.de")
    board = _board_with_links(uid)
    view = boards.view(who(uid), board)
    section = view.sections[0]
    order = [t.placement_id for t in section.tiles][::-1]

    target = boards.arrange(who(uid), board, view.version, {section.id: order})
    assert target == LayoutTarget.BOARD
    assert [t.placement_id for t in boards.view(who(uid), board).sections[0].tiles] == order


def test_stale_version_conflicts(app):
    uid = make_user("a@x.de")
    board = _board_with_links(uid, 1)
    view = boards.view(who(uid), board)
    boards.add_section(who(uid), board, view.version, "Two")
    with pytest.raises(Conflict):
        boards.add_section(who(uid), board, view.version, "Three")


def test_overlay_for_viewer(app):
    from app.enums import GranteeKind, ResourceKind, Right
    from app.services import shares

    owner = make_user("a@x.de")
    other = make_user("b@x.de")
    board = _board_with_links(owner)
    shares.grant(who(owner), ResourceKind.BOARD, board, GranteeKind.USER, other, Right.VIEW)
    view = boards.view(who(other), board)
    section = view.sections[0]
    original = [t.placement_id for t in section.tiles]
    order = list(reversed(original))

    assert boards.arrange(who(other), board, view.version, {section.id: order}) == LayoutTarget.OVERLAY
    assert [t.placement_id for t in boards.view(who(other), board).sections[0].tiles] == order
    assert [t.placement_id for t in boards.view(who(owner), board).sections[0].tiles] == original

    boards.fold(who(other), board, section.id, Fold.CLOSED)
    boards.resize(who(other), board, section.id, TileSize.SMALL)
    mine = boards.view(who(other), board).sections[0]
    assert mine.collapsed and mine.size == TileSize.SMALL
    boards.reset_overlay(who(other), board)
    assert not boards.view(who(other), board).has_overlay


def test_restore_revision(app):
    uid = make_user("a@x.de")
    board = _board_with_links(uid, 2)
    history = boards.history(who(uid), board)
    two_tiles = history[0]
    view = boards.view(who(uid), board)
    boards.unplace(who(uid), view.sections[0].tiles[0].placement_id, view.version)
    assert len(boards.view(who(uid), board).sections[0].tiles) == 1

    boards.restore(who(uid), board, two_tiles.id)
    assert len(boards.view(who(uid), board).sections[0].tiles) == 2


def test_board_page_and_fragment_render(app, browser):
    uid = make_user("a@x.de")
    board = _board_with_links(uid, 1)
    space = access.personal(who(uid)).id
    note = widgets.create(who(uid), space, "rss", "Feed", {"url": "https://example.org/feed"})
    view = boards.view(who(uid), board)
    boards.place(who(uid), view.sections[0].id, note, view.version)

    b = browser("a@x.de")
    page = b.get(f"/b/{board}")
    assert page.status_code == 200
    assert "L0" in page.text
    assert b.get(f"/b/{board}?edit").status_code == 200
    placements = boards.view(who(uid), board).sections[0].tiles
    for tile in placements:
        assert b.get(f"/w/{tile.placement_id}").status_code == 200


def test_editor_pages_render(app, browser):
    uid = make_user("a@x.de")
    board = _board_with_links(uid, 1)
    b = browser("a@x.de")
    space = access.personal(who(uid)).id
    for url in ["/library", "/connections", "/import", "/themes", "/teams", "/me", "/me/security",
                "/me/credentials", "/hints", "/widgets/new", f"/widgets/new?type=link&space={space}",
                f"/b/{board}/settings", f"/b/{board}/history", f"/spaces/{space}/code",
                f"/share/board/{board}", "/boards/new", "/styleguide"]:
        response = b.get(url)
        assert response.status_code == 200, (url, response.text[:500])


def test_widget_form_roundtrip(app, browser):
    uid = make_user("a@x.de")
    space = access.personal(who(uid)).id
    b = browser("a@x.de")
    response = b.post("/widgets/new", {"type": "link", "space_id": space, "title": "Git",
                                       "cfg.url": "https://git.example.org", "cfg.status": "http",
                                       "cfg.target": "newtab", "cfg.accept": "200, 401"})
    assert response.status_code == 303, response.text
    item = widgets.library(who(uid))[0]
    widget, _ = widgets.detail(who(uid), item.id)
    assert widget.config["accept"] == [200, 401]
    assert widget.config["info"] is None

    bad = b.post("/widgets/new", {"type": "link", "space_id": space, "title": "X", "cfg.url": "ftp://x"})
    assert bad.status_code == 400


def test_widget_preview_route(app, browser):
    uid = make_user("a@x.de")
    space = access.personal(who(uid)).id
    b = browser("a@x.de")
    response = b.post("/widget-preview", {"type": "note", "space_id": space, "title": "N", "cfg.text": "Hallo"})
    assert response.status_code == 200 and "Hallo" in response.text
    bad = b.post("/widget-preview", {"type": "link", "space_id": space, "title": "L"})
    assert bad.status_code == 200 and "callout-danger" in bad.text
