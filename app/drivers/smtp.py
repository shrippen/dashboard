"""Raw SMTP delivery.

SMTP_URL forms:
    smtp://user@host:587?starttls=true
    smtps://user@host:465
Password comes separately (Docker secret smtp_password).
"""

import smtplib
import ssl
from dataclasses import dataclass
from email.message import EmailMessage
from urllib.parse import parse_qs, unquote, urlparse

SMTP_PORT = 25
SMTPS_PORT = 465
TIMEOUT_S = 20
SCHEME_SSL = "smtps"


@dataclass(frozen=True)
class SmtpTarget:
    host: str
    port: int
    user: str | None
    use_ssl: bool
    starttls: bool


def parse(url: str) -> SmtpTarget:
    parts = urlparse(url)
    query = parse_qs(parts.query)
    use_ssl = parts.scheme == SCHEME_SSL
    return SmtpTarget(
        host=parts.hostname or "localhost",
        port=parts.port or (SMTPS_PORT if use_ssl else SMTP_PORT),
        user=unquote(parts.username) if parts.username else None,
        use_ssl=use_ssl,
        starttls=query.get("starttls", ["false"])[0].lower() == "true",
    )


def send(target: SmtpTarget, password: str | None, message: EmailMessage) -> None:
    context = ssl.create_default_context()
    if target.use_ssl:
        client = smtplib.SMTP_SSL(target.host, target.port, timeout=TIMEOUT_S, context=context)
    else:
        client = smtplib.SMTP(target.host, target.port, timeout=TIMEOUT_S)

    with client:
        if target.starttls:
            client.starttls(context=context)
        if target.user and password:
            client.login(target.user, password)
        client.send_message(message)
