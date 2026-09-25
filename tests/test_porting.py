"""YAML export/import and the Dashy conf.yml import."""

from app.services import access, boards, porting, widgets
from app.services.porting import ImportMode
from tests.conftest import make_user, who

DASHY = """
pageInfo:
  title: Homelab
  navLinks: [{title: Git, path: "https://git.lan"}]
appConfig:
  theme: nord
  statusCheck: true
  webSearch: {searchEngine: duckduckgo}
sections:
  - name: Media
    displayData: {collapsed: true, cols: 3, itemSize: small}
    items:
      - {title: Jellyfin, url: "https://jf.lan", icon: hl-jellyfin, hotkey: 1}
      - {title: Broken, url: "not-a-url"}
      - {title: FA, url: "https://fa.lan", icon: "fas fa-rocket", hideForGuests: true}
  - name: Info
    widgets:
      - {type: rss-feed, options: {rssUrl: "https://news.lan/feed", limit: 5}}
      - {type: clock, options: {timeZone: Europe/Berlin}}
      - {type: gl-current-cpu, options: {hostname: "http://glances.lan"}}
      - {type: crypto-watch-list}
"""


def test_dashy_import(app):
    uid = make_user("a@x.de")
    space = access.personal(who(uid)).id
    report = porting.import_dashy(who(uid), space, DASHY)

    assert report.boards == 1
    assert report.widgets == 4  # 2 links + rss + clock
    assert any("not-a-url" in s for s in report.skipped)
    assert any("crypto" in s for s in report.skipped)
    assert any("theme" in n for n in report.notes)
    assert any("hideForGuests" in n for n in report.notes)

    board = boards.visible(who(uid))[0]
    view = boards.view(who(uid), board.id)
    media = view.sections[0]
    assert media.collapsed and media.cols == 3
    assert [t.title for t in media.tiles] == ["Jellyfin", "FA"]
    assert media.tiles[0].config.status.value == "http"


def test_export_import_roundtrip(app):
    a = make_user("a@x.de")
    b = make_user("b@x.de")
    space_a = access.personal(who(a)).id
    porting.import_dashy(who(a), space_a, DASHY)
    text = porting.export_space(who(a), space_a)
    assert "Jellyfin" in text and "secret" not in text

    space_b = access.personal(who(b)).id
    report = porting.import_space(who(b), space_b, text, ImportMode.REPLACE)
    assert report.boards == 1 and report.widgets == 4
    assert len(widgets.library(who(b))) == 4


def test_yaml_error_has_line(app):
    import pytest

    uid = make_user("a@x.de")
    space = access.personal(who(uid)).id
    with pytest.raises(porting.PortError) as err:
        porting.import_space(who(uid), space, "boards: [\n  - name: x\n bad", ImportMode.MERGE)
    assert "line" in str(err.value)
