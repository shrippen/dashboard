"""Catalog completeness, theme contract and contrast."""

import re
from pathlib import Path

import pytest

from app.enums import Locale
from app.services import i18n, themes
from app.services.themes import ThemeError
from app.tools import stylecheck
from tests.conftest import make_user, who

TEMPLATES = Path(__file__).resolve().parent.parent / "app" / "web" / "templates"
# Static keys only: t('a.b') but not t('a.' ~ name).
STATIC_KEY = re.compile(r"(?<![\w.])t\('([a-z_][a-z_.0-9]*[a-z0-9_])'(?!\s*~)")


def test_catalogs_have_same_keys():
    de, en = set(i18n.catalog(Locale.DE)), set(i18n.catalog(Locale.EN))
    assert de - en == set(), "missing in en"
    assert en - de == set(), "missing in de"


def test_template_keys_exist():
    known = set(i18n.catalog(Locale.DE))
    missing = set()
    for path in TEMPLATES.rglob("*.html"):
        for key in STATIC_KEY.findall(path.read_text()):
            if key not in known:
                missing.add(f"{path.name}: {key}")
    assert not missing, sorted(missing)


def test_placeholders_and_fallback():
    assert i18n.t("hints.since", Locale.EN, when="2 days") == "since 2 days"
    assert i18n.t("no.such.key", Locale.EN) == "no.such.key"


def test_css_uses_tokens_only():
    assert stylecheck.main() == 0


def test_builtin_theme_contract(app):
    dark, light = themes.contract()
    assert dark["--bg-void"] == "#141312"
    assert light["--bg-void"] == "#f0e9d6"
    css, _version = themes.stylesheet(themes.ensure_builtin())
    assert ':root[data-theme="light"]' in css


def test_theme_values_are_validated(app):
    uid = make_user("a@x.de")
    space = next(iter(who(uid).spaces.values())).id
    theme = themes.duplicate(who(uid), themes.ensure_builtin(), space, "Mine")

    with pytest.raises(ThemeError):
        themes.update(who(uid), theme, "Mine", {"--bg-void": "url(https://evil)"}, {}, None)

    issues = themes.update(who(uid), theme, "Mine", {"--fg1": "#1e1e1e"}, {}, None)
    assert any(i.fg == "--fg1" for i in issues)


def test_builtin_theme_is_read_only(app):
    from app.services.access import AccessDenied

    uid = make_user("a@x.de")
    with pytest.raises(AccessDenied):
        themes.update(who(uid), themes.ensure_builtin(), "x", {}, {}, None)


def test_theme_zip_roundtrip(app):
    uid = make_user("a@x.de")
    space = next(iter(who(uid).spaces.values())).id
    theme = themes.duplicate(who(uid), themes.ensure_builtin(), space, "Blue")
    themes.update(who(uid), theme, "Blue", {"--blue": "#3366cc"}, {}, None)
    _name, blob = themes.export_zip(who(uid), theme)
    copy = themes.import_zip(who(uid), space, blob)
    loaded, _ = themes.get(who(uid), copy)
    assert loaded.dark["--blue"] == "#3366cc"
