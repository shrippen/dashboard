"""Configuration editor: boards, sections, widgets, connections, YAML, shares."""

from fastapi import APIRouter, Depends, File, Form, Request, UploadFile
from fastapi.responses import HTMLResponse, Response

from app.enums import (
    CredentialMode,
    GranteeKind,
    ResourceKind,
    Right,
    ServiceType,
    SortOrder,
    TeamRole,
    TileSize,
)
from app.services import access, boards, connections, icons, porting, shares, themes, widgets
from app.services.access import SpaceRef
from app.services.connections import ConnError, Tls
from app.services.porting import ImportMode, PortError
from app.services.shares import ShareError
from app.services.widgets import WidgetError
from app.web import deps, forms
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter()
YAML_TYPE = "application/yaml"


def _spaces(ctx: Ctx) -> list[SpaceRef]:
    return access.editable_spaces(ctx.who)


def _role(value: str) -> TeamRole | None:
    return TeamRole(value) if value else None


# ── Boards ──


@router.get("/boards/new")
def board_new(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/board_new.html", spaces=_spaces(ctx))


@router.post("/boards/new")
def board_create(name: str = Form(...), space_id: int = Form(...), ctx: Ctx = Depends(deps.require)):
    board_id = boards.create(ctx.who, space_id, name)
    return deps.redirect(f"/b/{board_id}?edit")


@router.get("/b/{board_id}/settings")
def board_settings(request: Request, board_id: int, ctx: Ctx = Depends(deps.require)):
    board = boards.view(ctx.who, board_id)
    if not board.can_edit:
        boards.ensure_editable(ctx.who, board_id)
    return page(request, ctx, "editor/board_settings.html", board=board,
                theme_list=themes.listing(ctx.who), roles=list(TeamRole))


@router.post("/b/{board_id}/settings")
def board_save(
    board_id: int,
    version: int = Form(...),
    name: str = Form(...),
    theme_id: str = Form(""),
    min_role: str = Form(""),
    ctx: Ctx = Depends(deps.require),
):
    boards.rename(ctx.who, board_id, version, name, int(theme_id) if theme_id else None, _role(min_role))
    return deps.redirect(f"/b/{board_id}?edit")


@router.post("/b/{board_id}/delete")
def board_delete(board_id: int, ctx: Ctx = Depends(deps.require)):
    boards.delete(ctx.who, board_id)
    return deps.redirect("/")


@router.post("/b/{board_id}/sections")
def section_add(board_id: int, version: int = Form(...), title: str = Form(""),
                ctx: Ctx = Depends(deps.require)):
    boards.add_section(ctx.who, board_id, version, title)
    return deps.redirect(f"/b/{board_id}?edit")


@router.post("/sections/{section_id}")
def section_save(
    section_id: int,
    board_id: int = Form(...),
    version: int = Form(...),
    title: str = Form(""),
    cols: str = Form(""),
    size: TileSize = Form(TileSize.MEDIUM),
    sort: SortOrder = Form(SortOrder.MANUAL),
    area: str = Form("main"),
    collapsed: str | None = Form(None),
    ctx: Ctx = Depends(deps.require),
):
    boards.edit_section(ctx.who, section_id, version, title=title.strip(),
                        cols=int(cols) if cols.isdigit() else None, size=size, sort=sort,
                        area=area if area in boards.AREAS else boards.AREAS[0],
                        collapsed=collapsed is not None)
    return deps.redirect(f"/b/{board_id}?edit")


@router.post("/sections/{section_id}/delete")
def section_delete(section_id: int, board_id: int = Form(...), version: int = Form(...),
                   ctx: Ctx = Depends(deps.require)):
    boards.delete_section(ctx.who, section_id, version)
    return deps.redirect(f"/b/{board_id}?edit")


@router.get("/sections/{section_id}/pick")
def section_pick(request: Request, section_id: int, board_id: int, version: int,
                 ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/pick.html", library=widgets.library(ctx.who),
                section_id=section_id, board_id=board_id, version=version)


@router.post("/sections/{section_id}/place")
def section_place(section_id: int, board_id: int = Form(...), version: int = Form(...),
                  widget_id: int = Form(...), ctx: Ctx = Depends(deps.require)):
    boards.place(ctx.who, section_id, widget_id, version)
    return deps.redirect(f"/b/{board_id}?edit")


@router.post("/placements/{placement_id}/delete")
def placement_delete(placement_id: int, board_id: int = Form(...), version: int = Form(...),
                     ctx: Ctx = Depends(deps.require)):
    boards.unplace(ctx.who, placement_id, version)
    return deps.redirect(f"/b/{board_id}?edit")


@router.get("/b/{board_id}/history")
def board_history(request: Request, board_id: int, ctx: Ctx = Depends(deps.require)):
    board = boards.view(ctx.who, board_id)
    return page(request, ctx, "editor/history.html", board=board,
                revisions=boards.history(ctx.who, board_id))


@router.post("/b/{board_id}/restore/{revision_id}")
def board_restore(board_id: int, revision_id: int, ctx: Ctx = Depends(deps.require)):
    boards.restore(ctx.who, board_id, revision_id)
    return deps.redirect(f"/b/{board_id}?edit")


@router.get("/b/{board_id}/export")
def board_export(board_id: int, ctx: Ctx = Depends(deps.require)):
    text = porting.export_board(ctx.who, board_id)
    return Response(text, media_type=YAML_TYPE,
                    headers={"Content-Disposition": f'attachment; filename="board-{board_id}.yml"'})


# ── Widgets ──


@router.get("/library")
def library(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/library.html", items=widgets.library(ctx.who),
                spaces=_spaces(ctx))


def _widget_form(request: Request, ctx: Ctx, kind: str, values: dict, *, space_id: int,
                 section_id: int | None, board_id: int | None, version: int | None,
                 widget=None, error: str | None = None, status: int = 200):
    schema = widgets.schema_of(kind)
    kind_def = next(k for k in widgets.type_list() if k.key == kind)
    return page(
        request, ctx, "editor/widget_form.html", status=status,
        kind=kind_def, form_fields=forms.fields(schema, values), widget=widget,
        space_id=space_id, section_id=section_id, board_id=board_id, version=version,
        conns=[c for c in connections.listing(ctx.who)
               if not kind_def.service or c.service == kind_def.service],
        all_conns=connections.listing(ctx.who), roles=list(TeamRole), error=error,
        title=(widget.title if widget else values.get("_title", "")),
    )


@router.get("/widgets/new")
def widget_new(request: Request, space: int = 0, section: int | None = None,
               board: int | None = None, version: int | None = None, type: str = "",
               ctx: Ctx = Depends(deps.require)):
    spaces = _spaces(ctx)
    space_id = space or (spaces[0].id if spaces else 0)
    if not type:
        return page(request, ctx, "editor/widget_type.html", kinds=widgets.type_list(),
                    space_id=space_id, section_id=section, board_id=board, version=version,
                    spaces=spaces)
    return _widget_form(request, ctx, type, {}, space_id=space_id, section_id=section,
                        board_id=board, version=version)


def _conn(value: str) -> int | None:
    return int(value) if value and value.isdigit() else None


@router.post("/widgets/new")
async def widget_create(request: Request, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    kind = str(form.get("type"))
    space_id = int(form.get("space_id") or 0)
    section_id = _conn(str(form.get("section_id") or ""))
    board_id = _conn(str(form.get("board_id") or ""))
    version = _conn(str(form.get("version") or ""))
    config = forms.parse(widgets.schema_of(kind), dict(form))
    title = str(form.get("title", ""))
    try:
        widget_id = widgets.create(ctx.who, space_id, kind, title, config,
                                   _conn(str(form.get("connection_id") or "")),
                                   _role(str(form.get("min_role") or "")))
    except WidgetError as exc:
        return _widget_form(request, ctx, kind, {**config, "_title": title}, space_id=space_id,
                            section_id=section_id, board_id=board_id, version=version,
                            error=str(exc), status=400)

    if section_id and board_id and version is not None:
        boards.place(ctx.who, section_id, widget_id, version)
        return deps.redirect(f"/b/{board_id}?edit")
    return deps.redirect("/library")


@router.get("/widgets/{widget_id}")
def widget_edit(request: Request, widget_id: int, board: int | None = None,
                ctx: Ctx = Depends(deps.require)):
    widget, granted = widgets.detail(ctx.who, widget_id)
    if granted < Right.EDIT:
        return page(request, ctx, "error.html", status=403, message="error.denied")
    return _widget_form(request, ctx, widget.type, widget.config, space_id=widget.space_id,
                        section_id=None, board_id=board, version=widget.version, widget=widget)


@router.post("/widgets/{widget_id}")
async def widget_save(request: Request, widget_id: int, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    widget, _granted = widgets.detail(ctx.who, widget_id)
    config = forms.parse(widgets.schema_of(widget.type), dict(form))
    board_id = _conn(str(form.get("board_id") or ""))
    try:
        widgets.update(ctx.who, widget_id, int(form.get("version") or 0), str(form.get("title", "")),
                       config, _conn(str(form.get("connection_id") or "")),
                       _role(str(form.get("min_role") or "")))
    except WidgetError as exc:
        return _widget_form(request, ctx, widget.type, config, space_id=widget.space_id,
                            section_id=None, board_id=board_id, version=widget.version,
                            widget=widget, error=str(exc), status=400)
    return deps.redirect(f"/b/{board_id}?edit" if board_id else "/library")


@router.post("/widgets/{widget_id}/delete")
def widget_delete(widget_id: int, ctx: Ctx = Depends(deps.require)):
    widgets.delete(ctx.who, widget_id)
    return deps.redirect("/library")


@router.post("/widgets/{widget_id}/copy")
def widget_copy(widget_id: int, space_id: int = Form(...), ctx: Ctx = Depends(deps.require)):
    new_id = widgets.copy(ctx.who, widget_id, space_id)
    return deps.redirect(f"/widgets/{new_id}")


@router.post("/widgets/preview")
async def widget_preview(request: Request, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    kind = str(form.get("type"))
    try:
        config = forms.parse(widgets.schema_of(kind), dict(form))
        frag = widgets.preview(ctx.who, int(form.get("space_id") or 0), kind,
                               str(form.get("title", "")), config,
                               _conn(str(form.get("connection_id") or "")))
    except WidgetError as exc:
        return page(request, ctx, "partials/error.html", message=str(exc), fragment=True)
    inline = next((k.inline for k in widgets.type_list() if k.key == kind), False)
    folder = "tiles" if inline else "widgets"
    return page(request, ctx, f"{folder}/{kind}.html", frag=frag, placement_id=0, fragment=True,
                preview=True)


@router.post("/icons/upload")
async def icon_upload(file: UploadFile = File(...), ctx: Ctx = Depends(deps.require)):
    body = await file.read()
    try:
        spec = icons.upload(body, file.content_type or "")
    except icons.IconError as exc:
        return HTMLResponse(str(exc), status_code=400)
    return HTMLResponse(spec)


# ── Connections ──


@router.get("/connections")
def connection_list(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/connections.html", items=connections.listing(ctx.who, Right.VIEW),
                spaces=_spaces(ctx), services=list(ServiceType), modes=list(CredentialMode))


@router.post("/connections/new")
def connection_create(
    request: Request,
    space_id: int = Form(...),
    service: ServiceType = Form(...),
    name: str = Form(""),
    url: str = Form(...),
    mode: CredentialMode = Form(CredentialMode.SHARED),
    secret: str = Form(""),
    insecure: str | None = Form(None),
    ctx: Ctx = Depends(deps.require),
):
    try:
        connections.create(ctx.who, space_id, service, name, url, mode, secret,
                           Tls.SKIP if insecure else Tls.VERIFY)
    except ConnError as exc:
        return page(request, ctx, "editor/connections.html", status=400, error=str(exc),
                    items=connections.listing(ctx.who, Right.VIEW), spaces=_spaces(ctx),
                    services=list(ServiceType), modes=list(CredentialMode))
    return deps.redirect("/connections")


@router.get("/connections/{conn_id}")
def connection_edit(request: Request, conn_id: int, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/connection_edit.html", conn=connections.get(ctx.who, conn_id),
                modes=list(CredentialMode), result=None)


@router.post("/connections/{conn_id}")
def connection_save(
    request: Request,
    conn_id: int,
    name: str = Form(""),
    url: str = Form(...),
    mode: CredentialMode = Form(...),
    secret: str = Form(""),
    insecure: str | None = Form(None),
    ctx: Ctx = Depends(deps.require),
):
    try:
        connections.update(ctx.who, conn_id, name, url, mode, secret or None,
                           Tls.SKIP if insecure else Tls.VERIFY)
    except ConnError as exc:
        return page(request, ctx, "editor/connection_edit.html", status=400, error=str(exc),
                    conn=connections.get(ctx.who, conn_id), modes=list(CredentialMode), result=None)
    return deps.redirect(f"/connections/{conn_id}")


@router.post("/connections/{conn_id}/test")
def connection_test(request: Request, conn_id: int, ctx: Ctx = Depends(deps.require)):
    result = connections.test(ctx.who, conn_id)
    return page(request, ctx, "partials/test_result.html", result=result, fragment=True)


@router.post("/connections/{conn_id}/delete")
def connection_delete(conn_id: int, ctx: Ctx = Depends(deps.require)):
    connections.delete(ctx.who, conn_id)
    return deps.redirect("/connections")


# ── Spaces: YAML, import ──


@router.get("/spaces/{space_id}/code")
def space_code(request: Request, space_id: int, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/code.html", space_id=space_id,
                text=porting.export_space(ctx.who, space_id), report=None, error=None)


@router.post("/spaces/{space_id}/code")
def space_code_save(request: Request, space_id: int, text: str = Form(...),
                    mode: ImportMode = Form(ImportMode.REPLACE), ctx: Ctx = Depends(deps.require)):
    try:
        report = porting.import_space(ctx.who, space_id, text, mode)
    except PortError as exc:
        return page(request, ctx, "editor/code.html", status=400, space_id=space_id, text=text,
                    report=None, error=str(exc))
    return page(request, ctx, "editor/code.html", space_id=space_id,
                text=porting.export_space(ctx.who, space_id), report=report, error=None)


@router.get("/spaces/{space_id}/export")
def space_export(space_id: int, ctx: Ctx = Depends(deps.require)):
    text = porting.export_space(ctx.who, space_id)
    return Response(text, media_type=YAML_TYPE,
                    headers={"Content-Disposition": f'attachment; filename="space-{space_id}.yml"'})


@router.get("/import")
def import_form(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/import.html", spaces=_spaces(ctx), report=None, error=None)


@router.post("/import")
async def import_run(request: Request, space_id: int = Form(...), kind: str = Form("yaml"),
                     file: UploadFile = File(...), ctx: Ctx = Depends(deps.require)):
    text = (await file.read()).decode("utf-8", "replace")
    try:
        if kind == "dashy":
            report = porting.import_dashy(ctx.who, space_id, text)
        else:
            report = porting.import_space(ctx.who, space_id, text, ImportMode.MERGE)
    except PortError as exc:
        return page(request, ctx, "editor/import.html", status=400, spaces=_spaces(ctx),
                    report=None, error=str(exc))
    return page(request, ctx, "editor/import.html", spaces=_spaces(ctx), report=report, error=None)


# ── Shares ──


@router.get("/share/{kind}/{resource_id}")
def share_dialog(request: Request, kind: ResourceKind, resource_id: int,
                 ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "editor/share.html", info=shares.info(ctx.who, kind, resource_id),
                rights=list(shares.SHAREABLE), error=None)


@router.post("/share/{kind}/{resource_id}")
def share_grant(request: Request, kind: ResourceKind, resource_id: int, grantee: str = Form(...),
                right: int = Form(...), ctx: Ctx = Depends(deps.require)):
    grantee_kind, _sep, grantee_id = grantee.partition(":")
    try:
        shares.grant(ctx.who, kind, resource_id, GranteeKind(grantee_kind), int(grantee_id), Right(right))
    except (ShareError, ValueError) as exc:
        return page(request, ctx, "editor/share.html", status=400,
                    info=shares.info(ctx.who, kind, resource_id), rights=list(shares.SHAREABLE),
                    error=str(exc))
    return deps.redirect(f"/share/{kind.value}/{resource_id}")


@router.post("/share/revoke/{share_id}")
def share_revoke(share_id: int, back: str = Form("/"), ctx: Ctx = Depends(deps.require)):
    shares.revoke(ctx.who, share_id)
    return deps.redirect(back if back.startswith("/") and not back.startswith("//") else "/")
