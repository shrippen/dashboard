"""Token API and embeds (e.g. an iframe in Dashy during the switch)."""

from fastapi import APIRouter, Request
from fastapi.responses import JSONResponse, Response

from app.enums import Severity, TokenScope
from app.services import boards, calendar, hints
from app.services.access import AccessDenied
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter()
UNAUTHORIZED = 401


def _who(request: Request, scope: TokenScope):
    who = deps.token_principal(request, scope)
    if who is None:
        raise AccessDenied("token")
    return who


@router.get("/api/summary")
def summary(request: Request):
    who = _who(request, TokenScope.READ)
    counts = hints.summary(who)
    return JSONResponse({
        "hints": {level.name.lower(): counts[level] for level in Severity},
        "boards": [{"id": b.id, "name": b.name} for b in boards.visible(who)],
    })


@router.get("/api/hints")
def hint_list(request: Request):
    who = _who(request, TokenScope.READ)
    return JSONResponse([
        {"id": h.id, "rule": h.rule, "severity": h.severity.name.lower(), "title": h.title,
         "why": h.why, "due": h.due, "url": h.action_url}
        for h in hints.active(who)
    ])


@router.get("/embed/hints")
def embed_hints(request: Request):
    who = deps.token_principal(request, TokenScope.EMBED) or _who(request, TokenScope.READ)
    ctx = Ctx(who, "", None, who.locale)
    return page(request, ctx, "embed/hints.html", items=hints.active(who, limit=10), embed=True)


@router.get("/embed/b/{board_id}")
def embed_board(request: Request, board_id: int):
    who = deps.token_principal(request, TokenScope.EMBED) or _who(request, TokenScope.READ)
    ctx = Ctx(who, "", None, who.locale)
    board = boards.view(who, board_id)
    return page(request, ctx, "embed/board.html", board=board, embed=True,
                token=request.query_params.get("token", ""))


@router.get("/calendar.ics")
def calendar_feed(request: Request):
    who = _who(request, TokenScope.READ)
    return Response(calendar.feed(who), media_type="text/calendar; charset=utf-8")
