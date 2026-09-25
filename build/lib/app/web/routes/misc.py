"""Icons, manifest, self-registration."""

import json

from fastapi import APIRouter, Form, Request
from fastapi.responses import Response

from app.enums import Locale
from app.services import admin, auth, icons
from app.services.accounts import AccountError
from app.services.admin import AdminError
from app.web import deps
from app.web.render import page
from app.web.routes.auth import _set_cookie

router = APIRouter()
ICON_CSP = "default-src 'none'; style-src 'unsafe-inline'"
ICON_CACHE = "public, max-age=86400"
NOT_FOUND = 404


@router.get("/icons/{key}")
def icon(key: str):
    found = icons.read(key)
    if found is None:
        return Response(status_code=NOT_FOUND)
    body, media = found
    return Response(body, media_type=media,
                    headers={"Content-Security-Policy": ICON_CSP, "Cache-Control": ICON_CACHE})


@router.get("/manifest.webmanifest")
def manifest():
    data = {
        "name": "dashboard",
        "short_name": "dashboard",
        "start_url": "/",
        "display": "standalone",
        "background_color": "#141312",
        "theme_color": "#141312",
        "icons": [{"src": "/static/icon.svg", "sizes": "any", "type": "image/svg+xml"}],
    }
    return Response(json.dumps(data), media_type="application/manifest+json")


@router.get("/register")
def register_form(request: Request):
    ctx = deps.context(request)
    if not admin.registration_open():
        return page(request, ctx, "error.html", status=NOT_FOUND, message="register.closed")
    return page(request, ctx, "auth/register.html", error=None)


@router.post("/register")
def register(request: Request, email: str = Form(...), name: str = Form(...),
             password: str = Form(...), locale: Locale = Form(Locale.DE)):
    ctx = deps.context(request)
    try:
        address = admin.register(email, name, password, locale)
    except (AccountError, AdminError) as exc:
        return page(request, ctx, "auth/register.html", status=400, error=str(exc))
    result = auth.login(address, password, deps.client_ip(request), deps.agent(request))
    return _set_cookie(deps.redirect("/"), result.token)
