"""Exception → response mapping."""

from http import HTTPStatus
from urllib.parse import quote

from fastapi import Request
from fastapi.responses import HTMLResponse, RedirectResponse

from app.web import deps
from app.web.render import page


def _next(request: Request) -> str:
    path = request.url.path
    if request.method != "GET" or deps.is_htmx(request):
        return "/"
    query = f"?{request.url.query}" if request.url.query else ""
    return quote(path + query, safe="/?=&")


def login_required(request: Request, _exc) -> HTMLResponse | RedirectResponse:
    if deps.is_htmx(request):
        response = HTMLResponse("", status_code=HTTPStatus.UNAUTHORIZED)
        response.headers["HX-Redirect"] = "/login"
        return response
    return RedirectResponse(f"/login?next={_next(request)}", status_code=303)


def totp_pending(request: Request, _exc) -> RedirectResponse:
    return RedirectResponse("/login/totp", status_code=303)


def _error(request: Request, status: HTTPStatus, key: str) -> HTMLResponse:
    ctx = deps.context(request)
    if deps.is_htmx(request):
        return page(request, ctx, "partials/error.html", status=status, message=key, fragment=True)
    return page(request, ctx, "error.html", status=status, message=key)


def csrf_failed(request: Request, _exc) -> HTMLResponse:
    return _error(request, HTTPStatus.FORBIDDEN, "error.csrf")


def denied(request: Request, _exc) -> HTMLResponse:
    return _error(request, HTTPStatus.FORBIDDEN, "error.denied")


def not_found(request: Request, _exc) -> HTMLResponse:
    return _error(request, HTTPStatus.NOT_FOUND, "error.not_found")


def conflict(request: Request, _exc) -> HTMLResponse:
    return _error(request, HTTPStatus.CONFLICT, "error.conflict")
