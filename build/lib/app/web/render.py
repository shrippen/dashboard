"""Template rendering with translation and formatting helpers."""

import re
from datetime import datetime
from pathlib import Path

from fastapi import Request
from fastapi.templating import Jinja2Templates
from markupsafe import Markup

from app.enums import Locale, Right, Severity
from app.services import accounts, boards, hints, i18n, icons, themes
from app.settings import get_settings
from app.web.deps import CSRF_FIELD, CSRF_HEADER, Ctx

TEMPLATES = Path(__file__).resolve().parent / "templates"
SEVERITY_TIER = {Severity.INFO: "blue", Severity.WARN: "yellow", Severity.CRITICAL: "red"}

templates = Jinja2Templates(directory=str(TEMPLATES))
env = templates.env
env.trim_blocks = True
env.lstrip_blocks = True


WEATHER_KINDS = [
    (0, "clear"), (3, "cloudy"), (48, "fog"), (57, "drizzle"), (67, "rain"),
    (77, "snow"), (82, "showers"), (86, "snow"), (99, "thunder"),
]


def _weather_kind(code: int | None) -> str:
    """WMO weather code → text key, e.g. 61 → "rain"."""
    if code is None:
        return "unknown"
    for limit, kind in WEATHER_KINDS:
        if code <= limit:
            return kind
    return "unknown"


HEX = re.compile(r"^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$")


def _hex6(value: str) -> str:
    """#abc → #aabbcc (the colour input needs six digits)."""
    digits = value.lstrip("#")
    if len(digits) == 3:
        digits = "".join(ch * 2 for ch in digits)
    return "#" + digits.lower()


def _iso(value: str | None):
    return datetime.fromisoformat(value) if value else None


def _monogram(title: str) -> str:
    words = [w for w in title.replace("-", " ").split() if w]
    letters = "".join(w[0] for w in words[:2]) if len(words) > 1 else title[:2]
    return letters.upper() or "?"


env.globals.update(
    Right=Right,
    Severity=Severity,
    SEVERITY_TIER=SEVERITY_TIER,
    CSRF_FIELD=CSRF_FIELD,
    CSRF_HEADER=CSRF_HEADER,
    monogram=_monogram,
    icon_url=icons.url_for,
    weather_kind=_weather_kind,
)
env.filters["iso"] = _iso
env.filters["is_hex"] = lambda v: bool(HEX.match(str(v or "")))
env.filters["hex6"] = _hex6


def page(request: Request, ctx: Ctx, template: str, /, status: int = 200, **values):
    """Render a full page or an HTMX fragment with the common context."""
    locale = ctx.locale

    def t(key: str, **params) -> str:
        return i18n.t(key, locale, **params)

    common = {
        "request": request,
        "ctx": ctx,
        "who": ctx.who,
        "t": t,
        "money": lambda v, c=i18n.DEFAULT_CURRENCY: i18n.money(v, locale, c),
        "num": lambda v, d=0: i18n.num(v, locale, d),
        "pct": lambda v: i18n.pct(v, locale),
        "day": lambda v: i18n.day(v, locale),
        "moment": lambda v: i18n.moment(v, locale),
        "ago": lambda v: i18n.ago(v, locale),
        "weekday": lambda v: i18n.weekday(v, locale),
        "csrf_input": Markup(f'<input type="hidden" name="{CSRF_FIELD}" value="{ctx.csrf}">'),
        "base_url": get_settings().base_url,
    }
    common.update(_chrome(ctx, values))
    common.update(values)
    return templates.TemplateResponse(request, template, common, status_code=status)


def _chrome(ctx: Ctx, values: dict) -> dict:
    """Navigation, theme and hint counts; skipped for fragments."""
    if values.get("fragment"):
        return {}

    board = values.get("board")
    theme_id = themes.active(
        ctx.who,
        getattr(board, "theme_id", None),
        board.space.id if board else None,
    )
    _css, version = themes.stylesheet(theme_id)
    result = {
        "theme_url": f"/theme/{theme_id}.css?v={version}",
        "color_mode": _color_mode(ctx),
        "nav_boards": [],
        "hint_counts": {},
    }
    if ctx.who is None:
        return result

    result["nav_boards"] = boards.visible(ctx.who)
    result["hint_counts"] = hints.summary(ctx.who)
    return result


def _color_mode(ctx: Ctx) -> str:
    if ctx.who is None:
        return ""

    mode = accounts.profile(ctx.who).color_mode.value
    return "" if mode == "auto" else mode


def locales() -> list[Locale]:
    return list(Locale)
