"""Personal settings: profile, security, tokens, personal credentials."""

import base64
import io

import qrcode
import qrcode.image.svg
from fastapi import APIRouter, Depends, Form, Request

from app.enums import ColorMode, Locale, TokenScope
from app.services import accounts, auth, boards, connections, themes
from app.services.accounts import AccountError
from app.services.auth import AuthError
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter(prefix="/me")


def _qr(uri: str) -> str:
    image = qrcode.make(uri, image_factory=qrcode.image.svg.SvgPathImage)
    buffer = io.BytesIO()
    image.save(buffer)
    return "data:image/svg+xml;base64," + base64.b64encode(buffer.getvalue()).decode()


@router.get("")
def profile(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "me/profile.html", profile=accounts.profile(ctx.who),
                boards_list=boards.visible(ctx.who), theme_list=themes.listing(ctx.who),
                locales=list(Locale), modes=list(ColorMode), saved=request.query_params.get("saved"))


@router.post("")
def profile_save(
    name: str = Form(...),
    locale: Locale = Form(...),
    color_mode: ColorMode = Form(...),
    theme_id: str = Form(""),
    start_board_id: str = Form(""),
    search_engine: str = Form(""),
    ctx: Ctx = Depends(deps.require),
):
    engine = search_engine.strip()
    if engine and not engine.startswith(("http://", "https://")):
        engine = ""
    accounts.update_profile(
        ctx.who,
        name=name.strip() or ctx.who.name,
        locale=locale,
        color_mode=color_mode,
        theme_id=int(theme_id) if theme_id else None,
        start_board_id=int(start_board_id) if start_board_id else None,
        search_engine=engine or None,
    )
    return deps.redirect("/me?saved=1")


@router.post("/password")
def password_save(request: Request, current: str = Form(""), new: str = Form(...),
                  ctx: Ctx = Depends(deps.require)):
    try:
        accounts.change_password(ctx.who, current, new, deps.client_ip(request))
    except AccountError as exc:
        return _security(request, ctx, error=str(exc))
    return deps.redirect("/me/security?saved=1")


def _security(request: Request, ctx: Ctx, **extra):
    return page(request, ctx, "me/security.html", profile=accounts.profile(ctx.who),
                sessions=auth.my_sessions(ctx.who), tokens=auth.my_tokens(ctx.who),
                boards_list=boards.visible(ctx.who), scopes=list(TokenScope),
                saved=request.query_params.get("saved"), **extra)


@router.get("/security")
def security(request: Request, ctx: Ctx = Depends(deps.require)):
    return _security(request, ctx)


@router.post("/totp/begin")
def totp_begin(request: Request, ctx: Ctx = Depends(deps.require)):
    secret, uri = auth.totp_begin(ctx.who)
    return _security(request, ctx, totp_secret=secret, totp_qr=_qr(uri))


@router.post("/totp/confirm")
def totp_confirm(request: Request, code: str = Form(...), ctx: Ctx = Depends(deps.require)):
    try:
        codes = auth.totp_confirm(ctx.who, code, deps.client_ip(request))
    except AuthError as exc:
        return _security(request, ctx, error=str(exc))
    return _security(request, ctx, recovery=codes)


@router.post("/totp/disable")
def totp_disable(request: Request, code: str = Form(""), ctx: Ctx = Depends(deps.require)):
    try:
        auth.totp_disable(ctx.who, code, deps.client_ip(request))
    except AuthError as exc:
        return _security(request, ctx, error=str(exc))
    return deps.redirect("/me/security?saved=1")


@router.post("/sessions/{session_id}/end")
def session_end(session_id: int, ctx: Ctx = Depends(deps.require)):
    auth.end_session(ctx.who, session_id)
    return deps.redirect("/me/security")


@router.post("/sessions/others/end")
def sessions_end(ctx: Ctx = Depends(deps.require)):
    auth.end_other_sessions(ctx.who)
    return deps.redirect("/me/security")


@router.post("/tokens")
async def token_create(request: Request, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    board_ids = [int(b) for b in form.getlist("boards") if str(b).isdigit()]
    days = str(form.get("days") or "")
    created = auth.create_token(ctx.who, str(form.get("name", "")),
                                TokenScope(str(form.get("scope") or TokenScope.READ)), board_ids,
                                int(days) if days.isdigit() else None)
    return _security(request, ctx, new_token=created.secret)


@router.post("/tokens/{token_id}/delete")
def token_delete(token_id: int, ctx: Ctx = Depends(deps.require)):
    auth.revoke_token(ctx.who, token_id)
    return deps.redirect("/me/security")


@router.get("/credentials")
def credentials(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "me/credentials.html", items=connections.personal_needed(ctx.who))


@router.post("/credentials/{conn_id}")
def credential_save(conn_id: int, secret: str = Form(...), ctx: Ctx = Depends(deps.require)):
    connections.set_mine(ctx.who, conn_id, secret)
    return deps.redirect("/me/credentials")


@router.post("/credentials/{conn_id}/delete")
def credential_delete(conn_id: int, ctx: Ctx = Depends(deps.require)):
    connections.drop_mine(ctx.who, conn_id)
    return deps.redirect("/me/credentials")
