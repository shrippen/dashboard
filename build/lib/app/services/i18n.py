"""Translations and locale-aware formatting.

Catalogs are YAML files keyed by dotted names, e.g. ``nav.start``.
Hints store a key plus parameters, so each reader sees his own language:

    t("hint.kimai.timer_long", Locale.EN, hours=11) -> "Timer running for 11 h"
"""

from datetime import date, datetime
from functools import lru_cache
from pathlib import Path

import yaml
from babel.dates import format_date, format_datetime, format_timedelta
from babel.numbers import format_currency, format_decimal, format_percent

from app.db.base import utcnow
from app.enums import Locale

CATALOG_DIR = Path(__file__).resolve().parent.parent / "i18n"
DEFAULT_LOCALE = Locale.DE
DEFAULT_CURRENCY = "EUR"


def _flatten(prefix: str, node, out: dict[str, str]) -> None:
    if isinstance(node, dict):
        for key, value in node.items():
            _flatten(f"{prefix}.{key}" if prefix else str(key), value, out)
        return

    out[prefix] = str(node)


@lru_cache
def catalog(locale: Locale) -> dict[str, str]:
    raw = yaml.safe_load((CATALOG_DIR / f"{locale.value}.yml").read_text(encoding="utf-8"))
    out: dict[str, str] = {}
    _flatten("", raw or {}, out)
    return out


class _Safe(dict):
    """Leave unknown placeholders visible instead of failing."""

    def __missing__(self, key: str) -> str:
        return "{" + key + "}"


def t(key: str, locale: Locale = DEFAULT_LOCALE, **params) -> str:
    text = catalog(locale).get(key) or catalog(DEFAULT_LOCALE).get(key) or key
    if not params:
        return text

    return text.format_map(_Safe(params))


def pick(accept_language: str | None) -> Locale:
    """First supported language of an Accept-Language header."""
    for part in (accept_language or "").split(","):
        code = part.split(";")[0].strip().lower()[:2]
        if code in Locale._value2member_map_:
            return Locale(code)

    return DEFAULT_LOCALE


def money(value: float, locale: Locale, currency: str = DEFAULT_CURRENCY) -> str:
    return format_currency(value, currency, locale=locale.value)


def num(value: float, locale: Locale, digits: int = 0) -> str:
    fmt = "#,##0" + ("." + "0" * digits if digits else "")
    return format_decimal(value, format=fmt, locale=locale.value)


def pct(value: float, locale: Locale) -> str:
    return format_percent(value, locale=locale.value)


def day(value: date | str | None, locale: Locale) -> str:
    if value is None:
        return ""

    if isinstance(value, str):
        value = date.fromisoformat(value[:10])

    return format_date(value, format="medium", locale=locale.value)


def weekday(value: date | str, locale: Locale) -> str:
    if isinstance(value, str):
        value = date.fromisoformat(value[:10])
    return format_date(value, format="EEE", locale=locale.value)


def moment(value: datetime | None, locale: Locale) -> str:
    if value is None:
        return ""

    return format_datetime(value, format="short", locale=locale.value)


def ago(value: datetime | None, locale: Locale) -> str:
    if value is None:
        return ""

    if value.tzinfo is None:
        value = value.replace(tzinfo=utcnow().tzinfo)

    delta = value - utcnow()
    return format_timedelta(delta, add_direction=True, locale=locale.value)
