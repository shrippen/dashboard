"""Shared HTTP client with timeouts and an egress guard.

Every outbound request of the app passes the guard. The admin can restrict
reachable networks, so invited users cannot scan the internal network
through status checks or feeds.
"""

import ipaddress
import socket
from collections.abc import Callable
from urllib.parse import urlparse

import httpx

CONNECT_TIMEOUT_S = 5.0
READ_TIMEOUT_S = 15.0
USER_AGENT = "dashboard/0.1 (+https://github.com/shrippen/dashboard)"
MAX_BODY = 5 * 1024 * 1024

Guard = Callable[[str, list[ipaddress._BaseAddress]], bool]


class EgressDenied(Exception):
    pass


class HttpError(Exception):
    """Transport or status failure with a short, secret-free message."""


_guard: Guard | None = None


def set_guard(guard: Guard | None) -> None:
    global _guard
    _guard = guard


def _addresses(host: str) -> list[ipaddress._BaseAddress]:
    try:
        infos = socket.getaddrinfo(host, None)
    except socket.gaierror as exc:
        raise HttpError(f"dns: {host}") from exc

    return [ipaddress.ip_address(info[4][0]) for info in infos]


def _check(url: str) -> None:
    if _guard is None:
        return

    host = urlparse(url).hostname or ""
    if not _guard(host, _addresses(host)):
        raise EgressDenied(host)


def request(
    method: str,
    url: str,
    *,
    headers: dict | None = None,
    params: dict | None = None,
    json: dict | None = None,
    data: dict | None = None,
    verify: bool = True,
    timeout: float = READ_TIMEOUT_S,
    follow: bool = True,
) -> httpx.Response:
    _check(url)
    merged = {"User-Agent": USER_AGENT, **(headers or {})}
    limits = httpx.Timeout(timeout, connect=CONNECT_TIMEOUT_S)
    try:
        with httpx.Client(verify=verify, timeout=limits, follow_redirects=follow) as client:
            response = client.request(
                method, url, headers=merged, params=params, json=json, data=data
            )
    except httpx.TimeoutException as exc:
        raise HttpError("timeout") from exc
    except httpx.HTTPError as exc:
        raise HttpError(type(exc).__name__) from exc

    if len(response.content) > MAX_BODY:
        raise HttpError("response too large")

    return response


def get_json(url: str, **kwargs) -> dict | list:
    response = request("GET", url, **kwargs)
    if response.status_code >= httpx.codes.BAD_REQUEST:
        raise HttpError(f"HTTP {response.status_code}")

    try:
        return response.json()
    except ValueError as exc:
        raise HttpError("invalid JSON") from exc
