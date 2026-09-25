"""Theme list, editor with live preview, import/export, stylesheet."""

from fastapi import APIRouter, Depends, File, Form, Request, UploadFile
from fastapi.responses import Response

from app.services import access, themes
from app.services.themes import ThemeError
from app.web import deps
from app.web.deps import Ctx
from app.web.render import page

router = APIRouter()
CSS_TYPE = "text/css"
CACHE_YEAR = "public, max-age=31536000, immutable"
TOKEN_GROUPS = {
    "background": ["--bg-void", "--bg-hard", "--bg0", "--bg-panel", "--bg1", "--bg2", "--nav-bg"],
    "text": ["--fg0", "--fg1", "--fg2", "--fg3", "--accent"],
    "semantic": ["--blue", "--blue-hover", "--aqua", "--green", "--yellow", "--orange", "--red", "--purple"],
    "roles": ["--field", "--score", "--hl", "--scrim", "--shadow"],
    "shape": ["--radius", "--chamfer", "--gutter", "--max-w", "--max-w-wide"],
    "fonts": ["--font-heading", "--font-sans", "--font-mono"],
}


@router.get("/theme/{theme_id}.css")
def stylesheet(theme_id: int):
    css, _version = themes.stylesheet(theme_id)
    return Response(css, media_type=CSS_TYPE, headers={"Cache-Control": CACHE_YEAR})


@router.get("/themes")
def theme_list(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "themes/list.html", items=themes.listing(ctx.who),
                spaces=access.editable_spaces(ctx.who))


@router.post("/themes/duplicate")
def theme_duplicate(theme_id: int = Form(...), space_id: int = Form(...), name: str = Form(...),
                    ctx: Ctx = Depends(deps.require)):
    new_id = themes.duplicate(ctx.who, theme_id, space_id, name)
    return deps.redirect(f"/themes/{new_id}")


def _editor(request: Request, ctx: Ctx, theme_id: int, **extra):
    theme, granted = themes.get(ctx.who, theme_id)
    base_dark, base_light = themes.contract()
    return page(request, ctx, "themes/edit.html", theme=theme, granted=granted,
                groups=TOKEN_GROUPS, base_dark=base_dark, base_light=base_light,
                dark={**base_dark, **(theme.dark or {})},
                light={**base_dark, **base_light, **(theme.light or {})},
                issues=themes.contrast_issues(theme.dark or {}, theme.light or {}), **extra)


@router.get("/themes/{theme_id}")
def theme_edit(request: Request, theme_id: int, ctx: Ctx = Depends(deps.require)):
    return _editor(request, ctx, theme_id)


def _tokens(form, prefix: str, base: dict) -> dict:
    """Only values that differ from the contract defaults are stored."""
    result = {}
    for name in themes.contract()[0]:
        value = str(form.get(prefix + name, "")).strip()
        if value and value != base.get(name):
            result[name] = value
    return result


@router.post("/themes/{theme_id}")
async def theme_save(request: Request, theme_id: int, ctx: Ctx = Depends(deps.require)):
    form = await request.form()
    base_dark, base_light = themes.contract()
    css = form.get("custom_css")
    try:
        themes.update(ctx.who, theme_id, str(form.get("name", "")),
                      _tokens(form, "dark", base_dark), _tokens(form, "light", {**base_dark, **base_light}),
                      str(css) if css is not None and ctx.who.is_admin else None)
    except ThemeError as exc:
        return _editor(request, ctx, theme_id, error=str(exc))
    return deps.redirect(f"/themes/{theme_id}")


@router.post("/themes/{theme_id}/delete")
def theme_delete(theme_id: int, ctx: Ctx = Depends(deps.require)):
    themes.delete(ctx.who, theme_id)
    return deps.redirect("/themes")


@router.get("/themes/{theme_id}/export")
def theme_export(theme_id: int, ctx: Ctx = Depends(deps.require)):
    name, blob = themes.export_zip(ctx.who, theme_id)
    return Response(blob, media_type="application/zip",
                    headers={"Content-Disposition": f'attachment; filename="{name}"'})


@router.post("/themes/import")
async def theme_import(request: Request, space_id: int = Form(...), file: UploadFile = File(...),
                       ctx: Ctx = Depends(deps.require)):
    try:
        new_id = themes.import_zip(ctx.who, space_id, await file.read())
    except ThemeError as exc:
        return page(request, ctx, "themes/list.html", status=400, items=themes.listing(ctx.who),
                    spaces=access.editable_spaces(ctx.who), error=str(exc))
    return deps.redirect(f"/themes/{new_id}")


@router.get("/styleguide")
def styleguide(request: Request, ctx: Ctx = Depends(deps.require)):
    return page(request, ctx, "styleguide.html")
