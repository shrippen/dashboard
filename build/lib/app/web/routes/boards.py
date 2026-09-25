"""Board pages, widget fragments, personal layout changes."""

import json

from fastapi import APIRouter, Depends, Form, Request
from fastapi.responses import HTMLResponse, Response

from app.enums import TileSize
from app.services import accounts, boards, hints
from app.services.boards import Fold, Visibility
from app.services.data import Freshness
from app.services.hints import HintAction
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter()
NO_CONTENT = 204


@router.get("/")
def home(request: Request, ctx: Ctx = Depends(deps.require)):
    board_id = boards.start_board(ctx.who, accounts.profile(ctx.who).start_board_id)
    return deps.redirect(f"/b/{board_id}")


@router.get("/b/{board_id}")
def board_page(request: Request, board_id: int, ctx: Ctx = Depends(deps.require)):
    board = boards.view(ctx.who, board_id)
    profile = accounts.profile(ctx.who)
    edit = request.query_params.get("edit") is not None and board.can_edit
    layer_edit = request.query_params.get("layout") is not None
    return page(request, ctx, "board.html", board=board, edit=edit,
                search_engine=profile.search_engine, layer_edit=layer_edit)


@router.get("/w/{placement_id}")
def widget_fragment(request: Request, placement_id: int, ctx: Ctx = Depends(deps.viewer)):
    fresh = Freshness.FORCE if request.query_params.get("refresh") else Freshness.CACHED
    frag = boards.fragment(ctx.who, placement_id, fresh)
    return page(request, ctx, f"widgets/{frag.type}.html", frag=frag, placement_id=placement_id,
                fragment=True)


@router.post("/b/{board_id}/arrange")
async def arrange(request: Request, board_id: int, ctx: Ctx = Depends(deps.require)):
    body = await request.json()
    layout = {int(k): [int(p) for p in v] for k, v in (body.get("layout") or {}).items()}
    target = boards.arrange(ctx.who, board_id, int(body.get("version", 0)), layout)
    return Response(json.dumps({"target": target.value}), media_type="application/json")


@router.post("/b/{board_id}/fold/{section_id}")
def fold(board_id: int, section_id: int, state: Fold = Form(...), ctx: Ctx = Depends(deps.require)):
    boards.fold(ctx.who, board_id, section_id, state)
    return Response(status_code=NO_CONTENT)


@router.post("/b/{board_id}/show/{placement_id}")
def show(board_id: int, placement_id: int, state: Visibility = Form(...),
         ctx: Ctx = Depends(deps.require)):
    boards.show(ctx.who, board_id, placement_id, state)
    return deps.redirect(f"/b/{board_id}?layout")


@router.post("/b/{board_id}/size/{section_id}")
def size(board_id: int, section_id: int, value: TileSize = Form(...),
         ctx: Ctx = Depends(deps.require)):
    boards.resize(ctx.who, board_id, section_id, value)
    return deps.redirect(f"/b/{board_id}?layout")


@router.post("/b/{board_id}/overlay/reset")
def reset_overlay(board_id: int, ctx: Ctx = Depends(deps.require)):
    boards.reset_overlay(ctx.who, board_id)
    return deps.redirect(f"/b/{board_id}")


@router.get("/hints")
def hint_page(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "hints.html", items=hints.active(ctx.who))


@router.post("/hints/{hint_id}/{action}")
def hint_action(request: Request, hint_id: int, action: HintAction, days: int = Form(7),
                ctx: Ctx = Depends(deps.require)):
    hints.act(ctx.who, hint_id, action, days)
    if deps.is_htmx(request):
        return HTMLResponse("")
    return deps.redirect("/hints")
