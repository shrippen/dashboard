"""Push adapter: Apprise URLs (ntfy://, gotify://, mailto://, tgram:// …)."""

import logging

import apprise

from app.settings import get_settings

log = logging.getLogger(__name__)

SENT: list[dict] = []


class NotifyFailed(Exception):
    pass


def send(url: str, title: str, body: str) -> None:
    if get_settings().dashboard_testing:
        SENT.append({"url": url, "title": title, "body": body})
        return

    target = apprise.Apprise()
    if not target.add(url):
        raise NotifyFailed("invalid url")
    if not target.notify(title=title, body=body):
        raise NotifyFailed("delivery failed")


def valid(url: str) -> bool:
    return apprise.Apprise().add(url)
