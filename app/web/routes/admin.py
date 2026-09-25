"""Administration: users, invitations, instance settings, audit log."""

from fastapi import APIRouter, Depends, Form, Request

from app.enums import InstanceRole, Locale, TeamRole
from app.services import admin, audit, invites, oidc, system, teams, themes
from app.services.access import AccessDenied
from app.services.admin import AdminError, Switch
from app.services.invites import InviteError
from app.services.oidc import GroupRule, OidcConfig, OidcError
from app.services.system import NetMode, NetworkPolicy
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter(prefix="/admin")


def _need_admin(ctx: Ctx) -> None:
    if not ctx.who.is_admin:
        raise AccessDenied("admin")


def _users_page(request: Request, ctx: Ctx, **extra):
    return page(request, ctx, "admin/users.html", rows=admin.user_rows(ctx.who),
                invites=invites.pending(ctx.who), team_list=teams.overview(ctx.who),
                roles=list(InstanceRole), team_roles=list(TeamRole), locales=list(Locale), **extra)


@router.get("/users")
def users_page(request: Request, ctx: Ctx = Depends(deps.require)):
    return _users_page(request, ctx)


@router.post("/invite")
async def invite(request: Request, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    team_ids = [t for t in form.getlist("teams") if t]
    names = {str(t.id): t.name for t in teams.overview(ctx.who)}
    role_for_team = TeamRole(str(form.get("team_role") or TeamRole.VIEWER))
    team_items = [{"team": names[t], "role": role_for_team.value} for t in team_ids if t in names]
    try:
        link = invites.create(ctx.who, str(form.get("email", "")),
                              InstanceRole(str(form.get("role") or InstanceRole.USER)), team_items,
                              Locale(str(form.get("locale") or Locale.DE)))
    except InviteError as exc:
        return _users_page(request, ctx, error=str(exc))
    return _users_page(request, ctx, invite_link=link)


@router.post("/invites/{invite_id}/delete")
def invite_delete(invite_id: int, ctx: Ctx = Depends(deps.require)):
    invites.revoke(ctx.who, invite_id)
    return deps.redirect("/admin/users")


@router.post("/users/{user_id}/role")
def user_role(request: Request, user_id: int, role: InstanceRole = Form(...),
              ctx: Ctx = Depends(deps.require)):
    try:
        admin.set_role(ctx.who, user_id, role, deps.client_ip(request))
    except AdminError as exc:
        return _users_page(request, ctx, error=str(exc))
    return deps.redirect("/admin/users")


@router.post("/users/{user_id}/active")
def user_active(request: Request, user_id: int, state: Switch = Form(...),
                ctx: Ctx = Depends(deps.require)):
    try:
        admin.set_active(ctx.who, user_id, state, deps.client_ip(request))
    except AdminError as exc:
        return _users_page(request, ctx, error=str(exc))
    return deps.redirect("/admin/users")


@router.post("/users/{user_id}/breakglass")
def user_breakglass(request: Request, user_id: int, state: Switch = Form(...),
                    ctx: Ctx = Depends(deps.require)):
    admin.set_breakglass(ctx.who, user_id, state, deps.client_ip(request))
    return deps.redirect("/admin/users")


@router.post("/users/{user_id}/delete")
def user_delete(request: Request, user_id: int, ctx: Ctx = Depends(deps.require)):
    try:
        admin.delete_user(ctx.who, user_id, deps.client_ip(request))
    except AdminError as exc:
        return _users_page(request, ctx, error=str(exc))
    return deps.redirect("/admin/users")


@router.post("/users/{user_id}/reset")
def user_reset(request: Request, user_id: int, ctx: Ctx = Depends(deps.require)):
    return _users_page(request, ctx, reset_link=invites.admin_reset_link(ctx.who, user_id))


@router.get("/users/{user_id}/reapply")
def user_reapply_preview(request: Request, user_id: int, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "admin/reapply.html", plan=oidc.preview_reapply(ctx.who, user_id),
                user_id=user_id)


@router.post("/users/{user_id}/reapply")
def user_reapply(request: Request, user_id: int, ctx: Ctx = Depends(deps.require)):
    oidc.reapply(ctx.who, user_id, deps.client_ip(request))
    return deps.redirect("/admin/users")


# ── Instance settings ──


def _settings_page(request: Request, ctx: Ctx, **extra):
    _need_admin(ctx)
    return page(
        request, ctx, "admin/settings.html",
        net=system.network(), net_modes=list(NetMode),
        iframe=", ".join(system.iframe_origins()),
        security=system.get_setting(system.SECURITY_KEY),
        registration=admin.registration_open(),
        location_shared=system.get_setting("dawarich_shared").get("allowed", False),
        oidc_cfg=oidc.config(), roles=list(InstanceRole), team_roles=list(TeamRole),
        theme_list=themes.listing(ctx.who),
        default_theme=system.get_setting(themes.DEFAULT_SETTING).get("id"),
        **extra,
    )


@router.get("/settings")
def settings_page(request: Request, ctx: Ctx = Depends(deps.require)):
    return _settings_page(request, ctx)


def _lines(text: str) -> list[str]:
    return [p.strip() for p in text.replace(",", "\n").splitlines() if p.strip()]


@router.post("/settings/network")
def settings_network(request: Request, mode: NetMode = Form(...), networks: str = Form(""),
                     hosts: str = Form(""), public: str | None = Form(None),
                     ctx: Ctx = Depends(deps.require)):
    try:
        system.set_network(ctx.who, NetworkPolicy(mode, _lines(networks), _lines(hosts), public is not None))
    except ValueError as exc:
        return _settings_page(request, ctx, error=str(exc))
    return deps.redirect("/admin/settings")


@router.post("/settings/general")
def settings_general(
    iframe: str = Form(""),
    force_admin_totp: str | None = Form(None),
    registration: str | None = Form(None),
    location_shared: str | None = Form(None),
    default_theme: str = Form(""),
    ctx: Ctx = Depends(deps.require),
):
    origins = [o for o in _lines(iframe) if o.startswith(("https://", "http://"))]
    system.put_setting(ctx.who, system.IFRAME_KEY, {"origins": origins})
    system.put_setting(ctx.who, system.SECURITY_KEY, {"force_admin_totp": force_admin_totp is not None})
    system.put_setting(ctx.who, admin.REGISTRATION_KEY, {"open": registration is not None})
    system.put_setting(ctx.who, "dawarich_shared", {"allowed": location_shared is not None})
    if default_theme:
        themes.set_default(ctx.who, int(default_theme))
    return deps.redirect("/admin/settings")


@router.post("/settings/oidc")
async def settings_oidc(request: Request, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    rules = []
    for group, role, team, team_role in zip(form.getlist("rule_group"), form.getlist("rule_role"),
                                            form.getlist("rule_team"), form.getlist("rule_team_role"),
                                            strict=False):
        if not str(group).strip():
            continue
        rules.append(GroupRule(str(group).strip(), InstanceRole(role) if role else None,
                               str(team).strip() or None, TeamRole(team_role or TeamRole.VIEWER)))
    cfg = OidcConfig(
        enabled=form.get("enabled") is not None,
        issuer=str(form.get("issuer", "")),
        client_id=str(form.get("client_id", "")),
        label=str(form.get("label", "")),
        only=form.get("only") is not None,
        auto_create=form.get("auto_create") is not None,
        email_link=form.get("email_link") is not None,
        rules=rules,
    )
    oidc.save(ctx.who, cfg, str(form.get("secret", "")) or None)
    return deps.redirect("/admin/settings")


@router.post("/settings/oidc/test")
def settings_oidc_test(request: Request, ctx: Ctx = Depends(deps.require)):
    try:
        issuer = oidc.test(ctx.who)
        result = {"ok": True, "message": issuer}
    except OidcError as exc:
        result = {"ok": False, "message": str(exc)}
    return page(request, ctx, "partials/test_result.html", result=result, fragment=True)


@router.get("/audit")
def audit_page(request: Request, ctx: Ctx = Depends(deps.require)):
    rows = admin.user_rows(ctx.who)
    return page(request, ctx, "admin/audit.html", entries=audit.entries(ctx.who),
                names={r.id: r.name for r in rows})
