"""Space isolation, team roles, shares, restrictions."""

import pytest

from app.enums import GranteeKind, InstanceRole, ResourceKind, Right, TeamRole
from app.services import access, boards, shares, widgets
from app.services.access import AccessDenied
from tests.conftest import make_user, who

LINK = {"url": "https://example.org", "status": "off"}


def _personal(uid):
    return access.personal(who(uid)).id


def _team_space(uid, name="IT"):
    return next(s.id for s in who(uid).spaces.values() if s.name == name)


def test_personal_spaces_are_isolated(app):
    a = make_user("a@x.de")
    b = make_user("b@x.de")
    board_a = boards.create(who(a), _personal(a), "A")

    with pytest.raises(AccessDenied):
        boards.view(who(b), board_a)
    assert board_a not in [x.id for x in boards.visible(who(b))]


def test_admin_cannot_read_personal_content(app):
    user = make_user("u@x.de")
    admin = make_user("admin@x.de", InstanceRole.ADMIN)
    board = boards.create(who(user), _personal(user), "Private")
    with pytest.raises(AccessDenied):
        boards.view(who(admin), board)


def test_team_roles(app):
    owner = make_user("o@x.de", teams=[{"team": "IT", "role": "owner"}])
    editor = make_user("e@x.de", teams=[{"team": "IT", "role": "editor"}])
    viewer = make_user("v@x.de", teams=[{"team": "IT", "role": "viewer"}])
    space = _team_space(owner)

    board = boards.create(who(editor), space, "Team")
    assert boards.view(who(viewer), board).can_edit is False
    assert boards.view(who(owner), board).can_edit is True
    with pytest.raises(AccessDenied):
        boards.create(who(viewer), space, "Nope")


def test_min_team_role_hides_widget(app):
    owner = make_user("o@x.de", teams=[{"team": "IT", "role": "owner"}])
    viewer = make_user("v@x.de", teams=[{"team": "IT", "role": "viewer"}])
    space = _team_space(owner)
    board = boards.create(who(owner), space, "Team")
    section = boards.view(who(owner), board).sections[0].id

    secret = widgets.create(who(owner), space, "link", "Revenue", LINK, min_role=TeamRole.OWNER)
    boards.place(who(owner), section, secret, boards.view(who(owner), board).version)

    assert len(boards.view(who(owner), board).sections[0].tiles) == 1
    assert boards.view(who(viewer), board).sections[0].tiles == []


def test_share_board_with_user(app):
    a = make_user("a@x.de")
    b = make_user("b@x.de")
    board = boards.create(who(a), _personal(a), "Shared")
    shares.grant(who(a), ResourceKind.BOARD, board, GranteeKind.USER, b, Right.VIEW)

    assert boards.view(who(b), board).can_edit is False
    with pytest.raises(AccessDenied):
        boards.rename(who(b), board, 2, "x", None, None)


def test_team_widget_on_personal_board(app):
    owner = make_user("o@x.de", teams=[{"team": "IT", "role": "owner"}])
    member = make_user("m@x.de", teams=[{"team": "IT", "role": "viewer"}])
    team_widget = widgets.create(who(owner), _team_space(owner), "link", "Wiki", LINK)

    board = boards.create(who(member), _personal(member), "Mine")
    view = boards.view(who(member), board)
    boards.place(who(member), view.sections[0].id, team_widget, view.version)
    assert boards.view(who(member), board).sections[0].tiles[0].title == "Wiki"

    # Leaving the team removes the widget from sight.
    from app.services import teams

    teams.remove_member(who(owner), next(iter(who(owner).teams)), member)
    assert boards.view(who(member), board).sections[0].tiles == []
