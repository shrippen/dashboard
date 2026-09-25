"""Themes: design tokens per mode, rendered to one stylesheet.

    theme = {dark: {--bg-void: #141312, ...}, light: {...overrides}, custom_css}
    css   = :root{dark} :root[data-theme=light]{light} @media(auto → light)

The token names of the shrippen design system are the theme contract.
Only shrippen ships; users duplicate it and change values.
"""

import hashlib
import io
import json
import re
import zipfile
from dataclasses import dataclass
from enum import StrEnum
from functools import lru_cache
from pathlib import Path

from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Theme
from app.enums import ResourceKind, Right, SpaceKind
from app.repos import content, misc, users
from app.services import access, audit
from app.services.access import AccessDenied, Principal
from app.services.util import NotFound, slug, unique

BUILTIN_DIR = Path(__file__).resolve().parent.parent / "themes"
BUILTIN_SLUG = "shrippen"
CONTRACT = 1
DEFAULT_SETTING = "theme_default"
MAX_CSS = 50_000
MAX_ZIP = 2 * 1024 * 1024

# Tokens the dashboard adds on top of the design system (components hardcode these).
EXTRA_DARK = {"--nav-bg": "rgba(20,19,18,.92)", "--shadow": "rgba(0,0,0,.35)"}
EXTRA_LIGHT = {"--nav-bg": "rgba(240,233,214,.92)", "--shadow": "rgba(60,56,54,.18)"}

_BLOCK = re.compile(r"(:root(?:\[data-theme=\"light\"\])?)\s*\{(.*?)\}", re.S)
_DECL = re.compile(r"(--[a-z0-9-]+)\s*:\s*([^;]+);", re.I)
_COMMENT = re.compile(r"/\*.*?\*/", re.S)
_SAFE_VALUE = re.compile(r"^[#a-zA-Z0-9 ,.'\"()%+\-/]*$")
_FORBIDDEN = ("url(", "expression", "@import", "javascript:", "\\")
_HEX = re.compile(r"^#([0-9a-f]{3}|[0-9a-f]{6})$", re.I)

AA_TEXT = 4.5
TEXT_PAIRS = [("--fg1", "--bg-void"), ("--fg1", "--bg-panel"), ("--fg2", "--bg-panel"),
              ("--fg0", "--bg-void"), ("--blue", "--bg-void"), ("--fg3", "--bg-panel")]


class ThemeError(ValueError):
    pass


class Mode(StrEnum):
    DARK = "dark"
    LIGHT = "light"


@dataclass
class ThemeRef:
    id: int
    slug: str
    name: str
    builtin: bool
    space_id: int | None
    can_edit: bool


@dataclass
class ContrastIssue:
    mode: Mode
    fg: str
    bg: str
    ratio: float


# ── Parsing and rendering ──


def parse_css(text: str) -> tuple[dict[str, str], dict[str, str]]:
    text = _COMMENT.sub("", text)
    dark: dict[str, str] = {}
    light: dict[str, str] = {}
    for selector, body in _BLOCK.findall(text):
        target = light if "light" in selector else dark
        for name, value in _DECL.findall(body):
            target[name] = " ".join(value.split())
    return dark, light


@lru_cache
def contract() -> tuple[dict[str, str], dict[str, str]]:
    """Token names and default values: the builtin theme plus dashboard extras."""
    dark, light = parse_css((BUILTIN_DIR / BUILTIN_SLUG / "tokens.css").read_text())
    return {**dark, **EXTRA_DARK}, {**light, **EXTRA_LIGHT}


def _check_value(name: str, value: str) -> str:
    value = " ".join(value.split())
    low = value.lower()
    if any(bad in low for bad in _FORBIDDEN) or not _SAFE_VALUE.match(value):
        raise ThemeError(f"theme.bad_value:{name}")
    return value


def clean_tokens(tokens: dict) -> dict[str, str]:
    known = set(contract()[0])
    result = {}
    for name, value in (tokens or {}).items():
        if name not in known:
            continue
        result[name] = _check_value(name, str(value))
    return result


def render(theme: Theme) -> str:
    base_dark, base_light = contract()
    dark = {**base_dark, **(theme.dark or {})}
    light = {**base_light, **(theme.light or {})}
    decl_dark = "".join(f"{k}:{v};" for k, v in dark.items())
    decl_light = "".join(f"{k}:{v};" for k, v in light.items())
    parts = [
        f":root{{{decl_dark}color-scheme:dark}}",
        f":root[data-theme=\"light\"]{{{decl_light}color-scheme:light}}",
        "@media (prefers-color-scheme: light){"
        f":root:not([data-theme]){{{decl_light}color-scheme:light}}}}",
    ]
    if theme.custom_css:
        parts.append(theme.custom_css)
    return "\n".join(parts)


# ── Contrast (WCAG) ──


def _luminance(hex_color: str) -> float:
    value = hex_color.lstrip("#")
    if len(value) == 3:
        value = "".join(ch * 2 for ch in value)
    channels = [int(value[i : i + 2], 16) / 255 for i in (0, 2, 4)]
    lin = [c / 12.92 if c <= 0.03928 else ((c + 0.055) / 1.055) ** 2.4 for c in channels]
    return 0.2126 * lin[0] + 0.7152 * lin[1] + 0.0722 * lin[2]


def ratio(fg: str, bg: str) -> float:
    a, b = sorted((_luminance(fg), _luminance(bg)), reverse=True)
    return round((a + 0.05) / (b + 0.05), 2)


def contrast_issues(dark: dict, light: dict) -> list[ContrastIssue]:
    base_dark, base_light = contract()
    issues = []
    modes = ((Mode.DARK, {**base_dark, **dark}), (Mode.LIGHT, {**base_dark, **base_light, **light}))
    for mode, tokens in modes:
        for fg, bg in TEXT_PAIRS:
            a, b = tokens.get(fg, ""), tokens.get(bg, "")
            if not (_HEX.match(a) and _HEX.match(b)):
                continue
            value = ratio(a, b)
            if value < AA_TEXT:
                issues.append(ContrastIssue(mode, fg, bg, value))
    return issues


# ── Builtin ──


def ensure_builtin() -> int:
    """Create or refresh the shipped shrippen theme from themes/shrippen/."""
    folder = BUILTIN_DIR / BUILTIN_SLUG
    meta = json.loads((folder / "theme.json").read_text())
    dark, light = parse_css((folder / "tokens.css").read_text())
    digest = hashlib.sha256(json.dumps([dark, light]).encode()).hexdigest()[:12]
    with session_scope() as s:
        theme = misc.builtin_theme(s, BUILTIN_SLUG)
        if theme is None:
            theme = misc.add(s, Theme(slug=BUILTIN_SLUG, name=meta["name"], builtin=True))
        if theme.digest != digest:
            theme.dark = {**dark, **EXTRA_DARK}
            theme.light = {**light, **EXTRA_LIGHT}
            theme.contract = CONTRACT
            theme.digest = digest
            theme.version += 1
        return theme.id


# ── Selection ──


def _default_id(s: Session) -> int | None:
    return misc.setting(s, DEFAULT_SETTING).get("id")


def active(who: Principal | None, board_theme: int | None = None, space_id: int | None = None) -> int:
    """Board forces > personal choice > team default > instance default > shrippen."""
    with session_scope() as s:
        candidates = [board_theme]
        if who is not None:
            user = users.get(s, who.user_id)
            candidates.append(user.theme_id if user else None)
        if space_id is not None:
            space = content.space(s, space_id)
            if space and space.kind == SpaceKind.TEAM:
                candidates.append((space.settings or {}).get("theme_id"))
        candidates.append(_default_id(s))

        for theme_id in candidates:
            if theme_id and misc.theme(s, theme_id):
                return theme_id

        return misc.builtin_theme(s, BUILTIN_SLUG).id


def stylesheet(theme_id: int) -> tuple[str, int]:
    """CSS and version (for cache busting); unknown ids fall back to shrippen."""
    with session_scope() as s:
        theme = misc.theme(s, theme_id) or misc.builtin_theme(s, BUILTIN_SLUG)
        return render(theme), theme.version


# ── Management ──


def _right(s: Session, who: Principal, theme: Theme) -> Right:
    if theme.builtin:
        return Right.USE
    space = access.space_of(s, who, theme.space_id)
    return access.right(who, ResourceKind.THEME, theme.id, space)


def listing(who: Principal) -> list[ThemeRef]:
    with session_scope() as s:
        result = []
        for theme in misc.themes(s, list(who.spaces)):
            granted = _right(s, who, theme)
            if granted < Right.USE:
                continue
            result.append(ThemeRef(theme.id, theme.slug, theme.name, theme.builtin,
                                   theme.space_id, granted >= Right.EDIT))
        return result


def get(who: Principal, theme_id: int) -> tuple[Theme, Right]:
    with session_scope() as s:
        theme = misc.theme(s, theme_id)
        if theme is None:
            raise NotFound("theme")
        granted = _right(s, who, theme)
        access.need(granted, Right.USE)
        return theme, granted


def duplicate(who: Principal, theme_id: int, space_id: int, name: str) -> int:
    with session_scope() as s:
        source = misc.theme(s, theme_id)
        if source is None:
            raise NotFound("theme")
        access.need(_right(s, who, source), Right.USE)
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)

        taken = {t.slug for t in misc.themes(s, [space_id]) if t.space_id == space_id}
        theme = misc.add(
            s,
            Theme(
                space_id=space_id,
                slug=unique(slug(name, "theme"), taken),
                name=name.strip() or source.name,
                dark=dict(source.dark or {}),
                light=dict(source.light or {}),
                custom_css=source.custom_css if who.is_admin else "",
            ),
        )
        audit.log(s, who.user_id, "theme.created", target=theme.name)
        return theme.id


def update(who: Principal, theme_id: int, name: str, dark: dict, light: dict,
           custom_css: str | None) -> list[ContrastIssue]:
    clean_dark, clean_light = clean_tokens(dark), clean_tokens(light)
    with session_scope() as s:
        theme = misc.theme(s, theme_id)
        if theme is None or theme.builtin:
            raise AccessDenied("theme.builtin")
        access.need(_right(s, who, theme), Right.EDIT)

        theme.name = name.strip() or theme.name
        theme.dark = clean_dark
        theme.light = clean_light
        if custom_css is not None:
            if not who.is_admin:
                raise AccessDenied("theme.css")
            if len(custom_css) > MAX_CSS or "@import" in custom_css.lower():
                raise ThemeError("theme.css_invalid")
            theme.custom_css = custom_css
        theme.version += 1

    return contrast_issues(clean_dark, clean_light)


def delete(who: Principal, theme_id: int) -> None:
    with session_scope() as s:
        theme = misc.theme(s, theme_id)
        if theme is None or theme.builtin:
            return
        access.need(_right(s, who, theme), Right.MANAGE)
        misc.drop_shares(s, ResourceKind.THEME, theme.id)
        misc.remove(s, theme)


def set_default(who: Principal, theme_id: int) -> None:
    if not who.is_admin:
        raise AccessDenied("theme.default")
    with session_scope() as s:
        misc.set_setting(s, DEFAULT_SETTING, {"id": theme_id})


def export_zip(who: Principal, theme_id: int) -> tuple[str, bytes]:
    theme, _granted = get(who, theme_id)
    meta = {"name": theme.name, "slug": theme.slug, "contract": theme.contract, "modes": ["dark", "light"]}
    tokens = ":root{\n" + "".join(f"  {k}: {v};\n" for k, v in (theme.dark or {}).items()) + "}\n"
    tokens += ':root[data-theme="light"]{\n' + "".join(
        f"  {k}: {v};\n" for k, v in (theme.light or {}).items()) + "}\n"

    buffer = io.BytesIO()
    with zipfile.ZipFile(buffer, "w", zipfile.ZIP_DEFLATED) as archive:
        archive.writestr("theme.json", json.dumps(meta, indent=2))
        archive.writestr("tokens.css", tokens)
        if theme.custom_css:
            archive.writestr("custom.css", theme.custom_css)
    return f"{theme.slug}.zip", buffer.getvalue()


def import_zip(who: Principal, space_id: int, blob: bytes) -> int:
    if len(blob) > MAX_ZIP:
        raise ThemeError("theme.zip_too_large")
    try:
        archive = zipfile.ZipFile(io.BytesIO(blob))
        meta = json.loads(archive.read("theme.json"))
        dark, light = parse_css(archive.read("tokens.css").decode())
        css = archive.read("custom.css").decode() if "custom.css" in archive.namelist() else ""
    except (zipfile.BadZipFile, KeyError, ValueError, UnicodeDecodeError) as exc:
        raise ThemeError("theme.zip_invalid") from exc

    new_id = duplicate(who, ensure_builtin(), space_id, str(meta.get("name", "Theme")))
    update(who, new_id, str(meta.get("name", "Theme")), dark, light, css if who.is_admin and css else None)
    return new_id
