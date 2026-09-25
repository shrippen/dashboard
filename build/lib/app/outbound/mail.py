"""Outgoing mail adapter: domain message → SMTP driver.

Delivery runs in a background thread so requests never wait for SMTP.
In tests (DASHBOARD_TESTING) messages land in OUTBOX instead.
"""

import logging
import threading
from dataclasses import dataclass
from email.message import EmailMessage
from email.utils import make_msgid

from app.drivers import smtp
from app.settings import get_settings

log = logging.getLogger(__name__)

OUTBOX: list["Mail"] = []


@dataclass
class Mail:
    to: str
    subject: str
    text: str
    html: str | None = None


def configured() -> bool:
    settings = get_settings()
    return bool(settings.smtp_url) or settings.dashboard_testing


def deliver(mail: Mail) -> None:
    settings = get_settings()
    if settings.dashboard_testing:
        OUTBOX.append(mail)
        return

    if not settings.smtp_url:
        log.info("mail skipped (no SMTP_URL): %s", mail.subject)
        return

    threading.Thread(target=_send, args=(mail,), daemon=True).start()


def _send(mail: Mail) -> None:
    settings = get_settings()
    message = EmailMessage()
    message["From"] = settings.smtp_from
    message["To"] = mail.to
    message["Subject"] = mail.subject
    message["Message-ID"] = make_msgid(domain="dashboard")
    message.set_content(mail.text)
    if mail.html:
        message.add_alternative(mail.html, subtype="html")

    try:
        smtp.send(smtp.parse(settings.smtp_url), settings.smtp_password, message)
    except Exception:  # SMTP errors must not break the app; they are logged.
        log.exception("mail to %s failed", mail.to)
