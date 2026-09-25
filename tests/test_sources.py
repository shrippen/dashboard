"""Web sources with mocked HTTP: feeds, status checks, SVG cleaning, OIDC mapping."""

import httpx

from app.enums import InstanceRole, TeamRole
from app.services.oidc import GroupRule, initial_values
from app.sources import icons
from app.sources.base import Ctx
from app.sources.web import Feed, HttpStatus

FEED = b"""<?xml version="1.0"?><rss version="2.0"><channel><title>News</title>
<item><title>First &lt;b&gt;bold&lt;/b&gt;</title><link>https://n.lan/1</link>
<description>&lt;script&gt;alert(1)&lt;/script&gt;Text</description>
<pubDate>Tue, 22 Sep 2026 10:00:00 +0000</pubDate></item>
<item><title>Evil</title><link>javascript:alert(1)</link></item></channel></rss>"""


def test_feed_is_sanitised(no_network):
    no_network.get("https://n.lan/feed").mock(return_value=httpx.Response(200, content=FEED))
    data = Feed().fetch(Ctx(params={"url": "https://n.lan/feed", "limit": 5}))

    first, evil = data["items"]
    assert "<" not in first["title"]
    assert "script" not in first["summary"]
    assert first["published"].startswith("2026-09-22")
    assert evil["link"] == ""


def test_status_check(no_network):
    no_network.get("https://up.lan").mock(return_value=httpx.Response(200))
    no_network.get("https://auth.lan").mock(return_value=httpx.Response(401))

    assert HttpStatus().fetch(Ctx(params={"url": "https://up.lan"}))["up"] is True
    assert HttpStatus().fetch(Ctx(params={"url": "https://auth.lan"}))["up"] is False
    assert HttpStatus().fetch(Ctx(params={"url": "https://auth.lan", "accept": [401]}))["up"] is True


def test_svg_cleaning():
    dirty = b'<svg onload="x()"><script>a()</script><use href="https://e/x.svg#a"/><use href="#ok"/></svg>'
    clean = icons.clean_svg(dirty).decode()
    assert "script" not in clean and "onload" not in clean
    assert "https://e" not in clean and "#ok" in clean


def test_oidc_initial_values():
    rules = [
        GroupRule("dashboard-admins", InstanceRole.ADMIN),
        GroupRule("team-it", None, "IT", TeamRole.EDITOR),
        GroupRule("it-leads", None, "IT", TeamRole.OWNER),
        GroupRule("office", None, "Büro", TeamRole.VIEWER),
    ]
    role, teams = initial_values(rules, ["team-it", "it-leads"])
    assert role == InstanceRole.USER
    assert teams == [{"team": "IT", "role": "owner"}]

    role, _ = initial_values(rules, ["dashboard-admins"])
    assert role == InstanceRole.ADMIN
