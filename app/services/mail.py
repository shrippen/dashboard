"""Compose account and security mails in the recipient's language."""

import hashlib
from dataclasses import dataclass
from pathlib import Path

from jinja2 import Environment, FileSystemLoader, select_autoescape

from app.db.base import session_scope
from app.enums import Locale
from app.outbound import mail as outbound
from app.repos import users
from app.services.i18n import t
from app.settings import get_settings

TEMPLATES = Path(__file__).resolve().parent.parent / "mail_templates"
KNOWN_DEVICES = "known_devices"
MAX_DEVICES = 20

_env = Environment(loader=FileSystemLoader(TEMPLATES), autoescape=select_autoescape(["html"]))


@dataclass
class Row:
    label: str
    text: str
    color: str = "#ebdbb2"


def configured() -> bool:
    return outbound.configured()


def render(
    to: str,
    locale: Locale,
    subject: str,
    paragraphs: list[str],
    button: tuple[str, str] | None = None,
    items: list[Row] | None = None,
) -> outbound.Mail:
    footer = t("mail.footer", locale, url=get_settings().base_url)
    html = _env.get_template("base.html").render(
        lang=locale.value,
        title=subject,
        paragraphs=paragraphs,
        items=items or [],
        button={"label": button[0], "url": button[1]} if button else None,
        footer=footer,
    )

    lines = [subject, "", *paragraphs]
    lines += [f"{row.label}  {row.text}" for row in items or []]
    if button:
        lines += ["", f"{button[0]}: {button[1]}"]
    lines += ["", footer]
    return outbound.Mail(to=to, subject=subject, text="\n".join(lines), html=html)


def send(mail: outbound.Mail) -> None:
    outbound.deliver(mail)


def invite(email: str, link: str, inviter: str, locale: Locale) -> None:
    send(
        render(
            email,
            locale,
            t("mail.invite.subject", locale),
            [t("mail.invite.body", locale, inviter=inviter)],
            (t("mail.invite.button", locale), link),
        )
    )


def reset(email: str, link: str, locale: Locale) -> None:
    send(
        render(
            email,
            locale,
            t("mail.reset.subject", locale),
            [t("mail.reset.body", locale)],
            (t("mail.reset.button", locale), link),
        )
    )


def security_notice(user_id: int, kind: str) -> None:
    with session_scope() as s:
        user = users.get(s, user_id)
        if user is None:
            return
        email, locale = user.email, Locale(user.locale)

    subject = t(f"mail.security.{kind}", locale)
    send(render(email, locale, subject, [subject, t("mail.security.hint", locale)]))


def new_login(user_id: int, ip: str, agent: str) -> None:
    """Mail only for devices not seen before (fingerprint of the user agent)."""
    device = hashlib.sha256(agent.encode()).hexdigest()[:16]
    with session_scope() as s:
        user = users.get(s, user_id)
        prefs = dict(user.prefs or {})
        known = list(prefs.get(KNOWN_DEVICES, []))
        if device in known:
            return

        known = [device, *known][:MAX_DEVICES]
        prefs[KNOWN_DEVICES] = known
        user.prefs = prefs
        email, locale, first = user.email, Locale(user.locale), len(known) == 1

    if first:
        return

    subject = t("mail.security.new_login", locale)
    body = t("mail.security.new_login_body", locale, ip=ip, agent=agent[:120])
    send(render(email, locale, subject, [body, t("mail.security.hint", locale)]))
