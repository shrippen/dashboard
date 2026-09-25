"""Application factory.

    uvicorn app.main:create_app --factory
"""

import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse
from fastapi.staticfiles import StaticFiles
from starlette.middleware.base import BaseHTTPMiddleware

from app.services import scheduler, system
from app.services.access import AccessDenied
from app.services.util import Conflict, NotFound
from app.settings import get_settings
from app.web import deps, errors
from app.web.routes import admin, api, auth, boards, editor, me, misc, teams, themes

STATIC = Path(__file__).resolve().parent / "static"
EMBED_PREFIX = "/embed/"


def _csp(path: str) -> str:
    frames = " ".join(system.iframe_origins()) or "'none'"
    ancestors = "*" if path.startswith(EMBED_PREFIX) else "'self'"
    return (
        "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "
        "img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; "
        f"frame-src {frames}; frame-ancestors {ancestors}; base-uri 'self'; form-action 'self'"
    )


class SecurityHeaders(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        response = await call_next(request)
        response.headers.setdefault("Content-Security-Policy", _csp(request.url.path))
        response.headers["X-Content-Type-Options"] = "nosniff"
        response.headers["Referrer-Policy"] = "same-origin"
        response.headers["Permissions-Policy"] = "camera=(), microphone=(), geolocation=()"
        return response


def create_app() -> FastAPI:
    settings = get_settings()
    logging.basicConfig(level=settings.log_level, format="%(levelname)s %(name)s %(message)s")
    system.start(settings)

    @asynccontextmanager
    async def lifespan(_app: FastAPI):
        if settings.scheduler_enabled and not settings.dashboard_testing:
            scheduler.start()
        yield
        scheduler.stop()

    app = FastAPI(docs_url=None, redoc_url=None, openapi_url=None, lifespan=lifespan)
    app.add_middleware(SecurityHeaders)
    app.mount("/static", StaticFiles(directory=STATIC), name="static")

    for module in (auth, boards, editor, me, teams, admin, themes, api, misc):
        app.include_router(module.router)

    app.add_exception_handler(deps.LoginRequired, errors.login_required)
    app.add_exception_handler(deps.TotpPending, errors.totp_pending)
    app.add_exception_handler(deps.CsrfFailed, errors.csrf_failed)
    app.add_exception_handler(AccessDenied, errors.denied)
    app.add_exception_handler(NotFound, errors.not_found)
    app.add_exception_handler(Conflict, errors.conflict)

    @app.get("/healthz", include_in_schema=False)
    def health() -> PlainTextResponse:
        return PlainTextResponse("ok")

    return app
