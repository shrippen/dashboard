"""Teams: members and roles; team space settings."""

from fastapi import APIRouter, Depends, Form, Request

from app.enums import HintAckMode, TeamRole
from app.services import admin, spaces, teams, themes
from app.services.teams import TeamError
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter()


def _page(request: Request, ctx: Ctx, **extra):
    all_users = admin.user_rows(ctx.who) if ctx.who.is_admin else []
    return page(request, ctx, "teams.html", team_list=teams.overview(ctx.who), roles=list(TeamRole),
                all_users=all_users, ack_modes=list(HintAckMode), theme_list=themes.listing(ctx.who),
                space_settings={t.space_id: spaces.settings(ctx.who, t.space_id)
                                for t in teams.overview(ctx.who)}, **extra)


@router.get("/teams")
def team_list(request: Request, ctx: Ctx = Depends(deps.require)):
    return _page(request, ctx)


@router.post("/teams")
def team_create(request: Request, name: str = Form(...), ctx: Ctx = Depends(deps.require)):
    try:
        teams.create(ctx.who, name, deps.client_ip(request))
    except TeamError as exc:
        return _page(request, ctx, error=str(exc))
    return deps.redirect("/teams")


@router.post("/teams/{team_id}/members")
def member_set(request: Request, team_id: int, user_id: int = Form(...), role: TeamRole = Form(...),
               ctx: Ctx = Depends(deps.require)):
    teams.set_member(ctx.who, team_id, user_id, role, deps.client_ip(request))
    return deps.redirect("/teams")


@router.post("/teams/{team_id}/members/{user_id}/delete")
def member_remove(request: Request, team_id: int, user_id: int, ctx: Ctx = Depends(deps.require)):
    teams.remove_member(ctx.who, team_id, user_id, deps.client_ip(request))
    return deps.redirect("/teams")


@router.post("/teams/{team_id}/delete")
def team_delete(request: Request, team_id: int, ctx: Ctx = Depends(deps.require)):
    teams.delete(ctx.who, team_id, deps.client_ip(request))
    return deps.redirect("/teams")


@router.post("/spaces/{space_id}/team-settings")
def team_settings(space_id: int, hint_ack: HintAckMode = Form(...), theme_id: str = Form(""),
                  ctx: Ctx = Depends(deps.require)):
    spaces.update_settings(ctx.who, space_id, {
        "hint_ack": hint_ack.value, "theme_id": int(theme_id) if theme_id else None})
    return deps.redirect("/teams")
