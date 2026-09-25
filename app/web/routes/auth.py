"""Setup, login, second factor, logout, invitations, password reset, OIDC."""

from urllib.parse import urlparse

from fastapi import APIRouter, Depends, Form, Request
from fastapi.responses import RedirectResponse

from app.enums import Locale
from app.services import accounts, auth, invites, oidc
from app.services.accounts import AccountError
from app.services.auth import AuthError, Step, Throttled
from app.settings import get_settings
from app.web import deps
from app.web.deps import COOKIE, Ctx
from app.web.render import page

router = APIRouter()


def _safe_next(target: str | None) -> str:
    """Only local paths: no open redirect via ?next=."""
    if not target or not target.startswith("/") or target.startswith("//"):
        return "/"
    return target


def _set_cookie(response: RedirectResponse, token: str) -> RedirectResponse:
    settings = get_settings()
    response.set_cookie(
        COOKIE,
        token,
        httponly=True,
        secure=settings.secure_cookies,
        samesite="lax",
        max_age=settings.session_absolute_hours * 3600,
        path="/",
    )
    return response


# ── Setup ──


@router.get("/setup")
def setup_form(request: Request):
    if not auth.setup_needed():
        return deps.redirect("/login")
    return page(request, deps.context(request), "auth/setup.html", error=None)


@router.post("/setup")
def setup_submit(
    request: Request,
    code: str = Form(...),
    email: str = Form(...),
    name: str = Form(""),
    password: str = Form(...),
    locale: Locale = Form(Locale.DE),
):
    ctx = deps.context(request)
    try:
        auth.create_admin(code, email, name, password, locale)
    except (AuthError, AccountError) as exc:
        return page(request, ctx, "auth/setup.html", status=400, error=str(exc), email=email, name=name)

    result = auth.login(email, password, deps.client_ip(request), deps.agent(request))
    return _set_cookie(deps.redirect("/"), result.token)


# ── Login ──


@router.get("/login")
def login_form(request: Request, next: str = "/"):
    if auth.setup_needed():
        return deps.redirect("/setup")

    ctx = deps.context(request)
    if ctx.who is not None:
        return deps.redirect(_safe_next(next))

    return page(request, ctx, "auth/login.html", error=None, next=_safe_next(next),
                oidc=oidc.button(), local_only=request.query_params.get("local") is not None)


@router.post("/login")
def login_submit(
    request: Request,
    email: str = Form(...),
    password: str = Form(...),
    next: str = Form("/"),
):
    ctx = deps.context(request)
    try:
        result = auth.login(email, password, deps.client_ip(request), deps.agent(request))
    except Throttled:
        return page(request, ctx, "auth/login.html", status=429, error="login.throttled",
                    next=next, email=email, oidc=oidc.button())
    except AuthError as exc:
        return page(request, ctx, "auth/login.html", status=401, error=str(exc),
                    next=next, email=email, oidc=oidc.button())

    target = "/login/totp?next=" + _safe_next(next) if result.step == Step.TOTP else _safe_next(next)
    return _set_cookie(deps.redirect(target), result.token)


@router.get("/login/totp")
def totp_form(request: Request, next: str = "/"):
    return page(request, deps.context(request), "auth/totp.html", error=None, next=_safe_next(next))


@router.post("/login/totp")
def totp_submit(request: Request, code: str = Form(...), next: str = Form("/")):
    token = request.cookies.get(COOKIE, "")
    try:
        auth.totp_verify(token, code, deps.client_ip(request), deps.agent(request))
    except AuthError:
        return page(request, deps.context(request), "auth/totp.html", status=401,
                    error="totp.invalid", next=next)
    return deps.redirect(_safe_next(next))


@router.post("/logout")
async def logout(request: Request):
    ctx = deps.context(request)
    await deps.check_csrf(request, ctx.csrf)
    id_token = auth.logout(request.cookies.get(COOKIE))
    target = oidc.logout_url(id_token) if id_token else "/login"
    response = deps.redirect(target or "/login")
    response.delete_cookie(COOKIE, path="/")
    return response


# ── Invitations ──


@router.get("/invite/{token}")
def invite_form(request: Request, token: str):
    ctx = deps.context(request)
    found = invites.peek(token)
    if found is None:
        return page(request, ctx, "error.html", status=404, message="invite.invalid")
    return page(request, ctx, "auth/invite.html", invite=found, token=token, error=None,
                oidc=oidc.button())


@router.post("/invite/{token}")
def invite_submit(
    request: Request,
    token: str,
    name: str = Form(...),
    password: str = Form(...),
    locale: Locale = Form(Locale.DE),
):
    ctx = deps.context(request)
    try:
        email = invites.accept(token, name, password, locale)
    except (AccountError, invites.InviteError) as exc:
        return page(request, ctx, "auth/invite.html", status=400, invite=invites.peek(token),
                    token=token, error=str(exc), oidc=oidc.button())

    result = auth.login(email, password, deps.client_ip(request), deps.agent(request))
    return _set_cookie(deps.redirect("/"), result.token)


# ── Password reset ──


@router.get("/reset")
def reset_request_form(request: Request):
    return page(request, deps.context(request), "auth/reset_request.html", sent=False)


@router.post("/reset")
def reset_request(request: Request, email: str = Form(...)):
    invites.request_reset(email, deps.client_ip(request))
    return page(request, deps.context(request), "auth/reset_request.html", sent=True)


@router.get("/reset/{token}")
def reset_form(request: Request, token: str):
    ctx = deps.context(request)
    if not invites.reset_valid(token):
        return page(request, ctx, "error.html", status=404, message="reset.invalid")
    return page(request, ctx, "auth/reset.html", token=token, error=None)


@router.post("/reset/{token}")
def reset_submit(request: Request, token: str, password: str = Form(...)):
    try:
        invites.reset(token, password, deps.client_ip(request))
    except (AccountError, invites.InviteError) as exc:
        return page(request, deps.context(request), "auth/reset.html", status=400,
                    token=token, error=str(exc))
    return deps.redirect("/login")


# ── OIDC (authentik) ──


@router.get("/auth/oidc/login")
def oidc_login(request: Request, next: str = "/"):
    url = oidc.authorize_url(request, _safe_next(next))
    return RedirectResponse(url, status_code=303)


@router.get("/auth/oidc/callback")
def oidc_callback(request: Request):
    try:
        user_id, id_token, target = oidc.complete(request)
    except oidc.OidcError as exc:
        return page(request, deps.context(request), "auth/login.html", status=401,
                    error=str(exc), next="/", oidc=oidc.button())

    token = auth.open_oidc_session(user_id, deps.client_ip(request), deps.agent(request), id_token)
    return _set_cookie(deps.redirect(_safe_next(target)), token)


@router.post("/me/oidc/link")
def oidc_link(request: Request, ctx: Ctx = Depends(deps.require)):
    return RedirectResponse(oidc.authorize_url(request, "/me", link_user=ctx.who.user_id), 303)


@router.post("/me/locale")
def set_locale(request: Request, locale: Locale = Form(...), ctx: Ctx = Depends(deps.require)):
    accounts.update_profile(ctx.who, locale=locale)
    return deps.redirect(_safe_next(urlparse(request.headers.get("referer", "/")).path))
