"""Request plumbing: session cookie, CSRF, current user, client address."""

from dataclasses import dataclass

from fastapi import Request
from fastapi.responses import RedirectResponse

from app.enums import AuthMethod, Locale, TokenScope
from app.services import auth, crypto, i18n
from app.services.access import Principal

COOKIE = "dsh_session"
CSRF_HEADER = "X-CSRF-Token"
CSRF_FIELD = "csrf"
SAFE_METHODS = {"GET", "HEAD", "OPTIONS"}
BEARER = "Bearer "


class LoginRequired(Exception):
    """Raised by require(); turned into a redirect to /login."""


class TotpPending(Exception):
    pass


class CsrfFailed(Exception):
    pass


@dataclass
class Ctx:
    """Everything a page needs about the requester."""

    who: Principal | None
    csrf: str
    method: AuthMethod | None
    locale: Locale


def client_ip(request: Request) -> str:
    # Behind the reverse proxy uvicorn's --proxy-headers sets request.client.
    return request.client.host if request.client else ""


def agent(request: Request) -> str:
    return request.headers.get("user-agent", "")


def _session(request: Request) -> auth.SessionInfo | None:
    cached = getattr(request.state, "session_info", None)
    if cached is not None:
        return cached or None

    info = auth.resolve(request.cookies.get(COOKIE))
    request.state.session_info = info or False
    return info


def context(request: Request) -> Ctx:
    info = _session(request)
    if info is None:
        locale = i18n.pick(request.headers.get("accept-language"))
        return Ctx(None, "", None, locale)

    locale = info.principal.locale if info.principal else i18n.pick(
        request.headers.get("accept-language")
    )
    return Ctx(info.principal, info.csrf, info.method, locale)


async def require(request: Request) -> Ctx:
    ctx = context(request)
    info = _session(request)
    if info is not None and info.pending_2fa:
        raise TotpPending()
    if ctx.who is None:
        raise LoginRequired()

    await check_csrf(request, ctx.csrf)
    return ctx


async def check_csrf(request: Request, expected: str) -> None:
    """Header (HTMX) or form field. Starlette caches the parsed form for the route."""
    if request.method in SAFE_METHODS:
        return

    sent = request.headers.get(CSRF_HEADER)
    if not sent:
        form = await request.form()
        sent = str(form.get(CSRF_FIELD, ""))
    if not sent or not expected or not crypto.same(sent, expected):
        raise CsrfFailed()


def viewer(request: Request) -> Ctx:
    """Session user, or an embed/read token for GET requests (iframes in other dashboards)."""
    ctx = context(request)
    if ctx.who is not None:
        return ctx

    who = token_principal(request, TokenScope.EMBED)
    if who is None:
        raise LoginRequired()
    return Ctx(who, "", None, who.locale)


def token_principal(request: Request, scope: TokenScope) -> Principal | None:
    header = request.headers.get("authorization", "")
    secret = header[len(BEARER):] if header.startswith(BEARER) else request.query_params.get("token")
    if not secret:
        return None
    return auth.principal_for_token(secret, scope)


def redirect(url: str) -> RedirectResponse:
    return RedirectResponse(url, status_code=303)


def is_htmx(request: Request) -> bool:
    return request.headers.get("HX-Request") == "true"
