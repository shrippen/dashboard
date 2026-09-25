"""YAML import/export of a space, and the Dashy conf.yml import.

Export format (credentials are never included):

    space: team:IT
    settings: {...}
    connections: [{id, type, name, url, credentials: shared|personal, options}]
    widgets:     [{id, type, title, config, connection}]
    boards:      [{name, slug, sections: [{title, cols, size, widgets: [key | team:X/key]}]}]
"""

from dataclasses import dataclass, field
from enum import StrEnum

import yaml
from pydantic import ValidationError
from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Board, Connection, Placement, Section, Widget
from app.enums import (
    CredentialMode,
    LinkTarget,
    Right,
    ServiceType,
    SortOrder,
    SpaceKind,
    TeamRole,
    TileSize,
)
from app.repos import content, users
from app.services import access
from app.services.access import Principal
from app.services.util import slug, unique
from app.widgets import base as types

REF_SEP = "/"
TEAM_PREFIX = "team:"
INSTANCE_PREFIX = "instance"
DASHY_ICON_PREFIXES = ("si-", "hl-", "favicon", "http://", "https://")
DASHY_WIDGETS = {
    "rss-feed": "rss",
    "clock": "clock",
    "weather": "weather",
    "weather-forecast": "weather",
    "iframe": "iframe",
    "public-ip": "public_ip",
}
DASHY_GLANCES = "gl-"
MAX_IMPORT_BYTES = 2 * 1024 * 1024


class ImportMode(StrEnum):
    MERGE = "merge"
    REPLACE = "replace"


class PortError(ValueError):
    pass


@dataclass
class Report:
    boards: int = 0
    widgets: int = 0
    connections: int = 0
    skipped: list[str] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)


# ── Serialize ──


def widget_dict(widget: Widget, conn_key: str | None) -> dict:
    item = {"id": widget.key, "type": widget.type, "title": widget.title, "config": widget.config}
    if conn_key:
        item["connection"] = conn_key
    if widget.min_team_role:
        item["min_team_role"] = TeamRole(widget.min_team_role).value
    return item


def _ref(widget: Widget, home_space_id: int, spaces: dict) -> str:
    if widget.space_id == home_space_id:
        return widget.key

    space = spaces.get(widget.space_id)
    if space is None:
        return widget.key
    if space.kind == SpaceKind.INSTANCE:
        return f"{INSTANCE_PREFIX}{REF_SEP}{widget.key}"
    return f"{TEAM_PREFIX}{space.name}{REF_SEP}{widget.key}"


def board_dict(board: Board, spaces: dict) -> dict:
    sections = []
    for section in board.sections:
        item = {
            "title": section.title,
            "widgets": [_ref(p.widget, board.space_id, spaces) for p in section.placements],
        }
        if section.cols:
            item["cols"] = section.cols
        if section.size != TileSize.MEDIUM:
            item["size"] = TileSize(section.size).value
        if section.sort != SortOrder.MANUAL:
            item["sort"] = SortOrder(section.sort).value
        if section.collapsed:
            item["collapsed"] = True
        if section.area != "main":
            item["area"] = section.area
        sections.append(item)

    return {"name": board.name, "slug": board.slug, "sections": sections}


def _conn_dict(conn: Connection) -> dict:
    item = {
        "id": conn.key,
        "type": conn.service,
        "name": conn.name,
        "url": conn.url,
        "credentials": CredentialMode(conn.credential_mode).value,
    }
    if conn.options:
        item["options"] = conn.options
    if not conn.verify_tls:
        item["verify_tls"] = False
    return item


def _space_label(space) -> str:
    if space.kind == SpaceKind.TEAM:
        return f"{TEAM_PREFIX}{space.name}"
    return SpaceKind(space.kind).value


def export_space(who: Principal, space_id: int) -> str:
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)
        raw = content.space(s, space_id)
        spaces = {sp.id: sp for sp in content.all_spaces(s)}
        conns = content.connections(s, [space_id])
        conn_keys = {c.id: c.key for c in conns}

        doc = {
            "space": _space_label(raw),
            "settings": raw.settings or {},
            "connections": [_conn_dict(c) for c in conns],
            "widgets": [
                widget_dict(w, conn_keys.get(w.connection_id))
                for w in content.widgets(s, [space_id])
            ],
            "boards": [
                board_dict(content.board(s, b.id), spaces) for b in content.boards(s, [space_id])
            ],
        }

    return yaml.safe_dump(doc, allow_unicode=True, sort_keys=False)


def export_board(who: Principal, board_id: int) -> str:
    with session_scope() as s:
        board = content.board(s, board_id)
        space = access.space_of(s, who, board.space_id)
        access.need(access.space_right(who, space), Right.VIEW)
        spaces = {sp.id: sp for sp in content.all_spaces(s)}
        doc = board_dict(board, spaces)
    return yaml.safe_dump(doc, allow_unicode=True, sort_keys=False)


# ── Import ──


def load_yaml(text: str) -> dict:
    if len(text.encode()) > MAX_IMPORT_BYTES:
        raise PortError("import.too_large")

    try:
        doc = yaml.safe_load(text) or {}
    except yaml.YAMLError as exc:
        mark = getattr(exc, "problem_mark", None)
        line = mark.line + 1 if mark else 0
        raise PortError(f"yaml: line {line}") from exc

    if not isinstance(doc, dict):
        raise PortError("import.not_mapping")

    return doc


def import_space(who: Principal, space_id: int, text: str, mode: ImportMode) -> Report:
    doc = load_yaml(text)
    report = Report()
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        access.need(access.space_right(who, space), Right.EDIT)
        if mode == ImportMode.REPLACE:
            _clear(s, space_id)

        raw = content.space(s, space_id)
        if isinstance(doc.get("settings"), dict):
            raw.settings = {**(raw.settings or {}), **doc["settings"]}
            raw.version += 1

        conn_ids = _import_connections(s, space_id, doc.get("connections") or [], report)
        keys = _import_widgets(s, space_id, doc.get("widgets") or [], conn_ids, report)
        for item in doc.get("boards") or []:
            _import_board(s, who, space_id, item, keys, report)

    return report


def _clear(s: Session, space_id: int) -> None:
    for board in content.boards(s, [space_id]):
        content.remove(s, board)
    for widget in content.widgets(s, [space_id]):
        content.remove(s, widget)


def _import_connections(s: Session, space_id: int, items: list, report: Report) -> dict[str, int]:
    found = {c.key: c.id for c in content.connections(s, [space_id])}
    for item in items:
        key = str(item.get("id") or slug(item.get("name", "conn")))
        if key in found:
            continue
        try:
            service = ServiceType(item["type"]).value
        except (KeyError, ValueError):
            report.skipped.append(f"connection {key}: type")
            continue

        conn = content.add(
            s,
            Connection(
                space_id=space_id,
                key=key,
                name=item.get("name") or key,
                service=service,
                url=str(item.get("url", "")).rstrip("/"),
                credential_mode=CredentialMode(item.get("credentials", CredentialMode.SHARED)),
                options=item.get("options") or {},
                verify_tls=item.get("verify_tls", True),
            ),
        )
        found[key] = conn.id
        report.connections += 1
        report.notes.append(f"connection {key}: token missing")
    return found


def _import_widgets(
    s: Session, space_id: int, items: list, conn_ids: dict[str, int], report: Report
) -> dict[str, int]:
    keys = {w.key: w.id for w in content.widgets(s, [space_id])}
    for item in items:
        kind = types.get(str(item.get("type")))
        key = str(item.get("id") or slug(item.get("title", "widget")))
        if kind is None:
            report.skipped.append(f"widget {key}: type {item.get('type')}")
            continue
        try:
            config = kind.schema.model_validate(item.get("config") or {})
        except ValidationError as exc:
            report.skipped.append(f"widget {key}: {exc.errors()[0].get('msg')}")
            continue

        key = unique(key, set(keys))
        widget = content.add(
            s,
            Widget(
                space_id=space_id,
                key=key,
                type=kind.key,
                title=str(item.get("title", "")),
                config=config.model_dump(mode="json"),
                connection_id=conn_ids.get(item.get("connection")),
                min_team_role=item.get("min_team_role"),
            ),
        )
        keys[key] = widget.id
        report.widgets += 1
    return keys


def _resolve_ref(s: Session, who: Principal, ref: str, local: dict[str, int]) -> int | None:
    if REF_SEP not in ref:
        return local.get(ref)

    scope, key = ref.rsplit(REF_SEP, 1)
    if scope == INSTANCE_PREFIX:
        space = content.instance_space(s)
    elif scope.startswith(TEAM_PREFIX):
        team = users.team_by_name(s, scope[len(TEAM_PREFIX):])
        space = content.team_space(s, team.id) if team else None
    else:
        space = None

    if space is None or space.id not in who.spaces:
        return None

    widget = content.widget_by_key(s, space.id, key)
    return widget.id if widget else None


def resolve(s: Session, who: Principal, space_id: int, ref: str) -> int | None:
    """Widget id for a board reference, relative to the board's space."""
    if REF_SEP in ref:
        return _resolve_ref(s, who, ref, {})

    widget = content.widget_by_key(s, space_id, ref)
    return widget.id if widget else None


def _import_board(
    s: Session, who: Principal, space_id: int, item: dict, keys: dict[str, int], report: Report
) -> None:
    taken = {b.slug for b in content.boards(s, [space_id])}
    name = str(item.get("name") or "Board")
    board = content.add(
        s,
        Board(
            space_id=space_id,
            slug=unique(slug(item.get("slug") or name, "board"), taken),
            name=name,
            position=len(taken),
        ),
    )
    for index, raw in enumerate(item.get("sections") or []):
        section = content.add(
            s,
            Section(
                board_id=board.id,
                title=str(raw.get("title", "")),
                position=index,
                cols=raw.get("cols"),
                size=TileSize(raw.get("size", TileSize.MEDIUM)),
                sort=SortOrder(raw.get("sort", SortOrder.MANUAL)),
                collapsed=bool(raw.get("collapsed", False)),
                area=raw.get("area", "main"),
            ),
        )
        for pos, ref in enumerate(raw.get("widgets") or []):
            widget_id = _resolve_ref(s, who, str(ref), keys)
            if widget_id is None:
                report.skipped.append(f"board {name}: widget {ref}")
                continue
            content.add(s, Placement(section_id=section.id, widget_id=widget_id, position=pos))
    report.boards += 1


# ── Dashy ──


def dashy_to_doc(text: str) -> tuple[dict, Report]:
    """Translate a Dashy conf.yml into our import format."""
    raw = load_yaml(text)
    report = Report()
    widgets: list[dict] = []
    sections: list[dict] = []
    taken: set[str] = set()
    app_config = raw.get("appConfig") or {}
    default_status = bool(app_config.get("statusCheck", False))

    for key in ("theme", "customCss", "layout", "iconSize", "cssThemes", "colors"):
        if key in app_config:
            report.notes.append(f"appConfig.{key}: ignored (theme stays shrippen)")
    if app_config.get("auth"):
        report.notes.append("appConfig.auth: recreate users and permissions in the dashboard")

    for sec in raw.get("sections") or []:
        refs = []
        for entry in sec.get("items") or []:
            item = _dashy_item(entry, default_status, report)
            if item is None:
                continue
            item["id"] = unique(slug(item["title"] or "link", "link"), taken)
            taken.add(item["id"])
            widgets.append(item)
            refs.append(item["id"])
            for field_name in ("hideForUsers", "showForUsers", "hideForGuests", "hideForKeycloakUsers"):
                if entry.get(field_name):
                    report.notes.append(f"{item['title']}: {field_name} → set permissions manually")

        for entry in sec.get("widgets") or []:
            item = _dashy_widget(entry, report)
            if item is None:
                continue
            item["id"] = unique(slug(item["title"] or item["type"], item["type"]), taken)
            taken.add(item["id"])
            widgets.append(item)
            refs.append(item["id"])

        display = sec.get("displayData") or {}
        section = {"title": sec.get("name", ""), "widgets": refs}
        if display.get("collapsed"):
            section["collapsed"] = True
        if display.get("cols"):
            section["cols"] = int(display["cols"])
        size = display.get("itemSize")
        if size in TileSize._value2member_map_:
            section["size"] = size
        if display.get("sortBy") == "alphabetical":
            section["sort"] = SortOrder.ALPHABETICAL.value
        sections.append(section)

    page = raw.get("pageInfo") or {}
    doc = {
        "settings": _dashy_settings(page, app_config),
        "widgets": widgets,
        "boards": [{"name": page.get("title") or "Start", "slug": "start", "sections": sections}],
    }
    for sub in raw.get("pages") or []:
        report.notes.append(f"page {sub.get('name')}: import its YAML file separately")
    return doc, report


def _dashy_settings(page: dict, app_config: dict) -> dict:
    settings: dict = {}
    if page.get("title"):
        settings["title"] = page["title"]
    if page.get("description"):
        settings["description"] = page["description"]
    nav = [{"title": n.get("title", ""), "url": n.get("path", "")} for n in page.get("navLinks") or []]
    if nav:
        settings["nav"] = nav
    if page.get("footerText"):
        settings["footer"] = page["footerText"]
    engine = (app_config.get("webSearch") or {}).get("customSearchEngine")
    if engine:
        settings["search_engine"] = engine
    return settings


def _dashy_icon(icon: str) -> str:
    if not icon:
        return ""
    if icon.startswith(DASHY_ICON_PREFIXES):
        return icon
    return ""


def _dashy_item(entry: dict, default_status: bool, report: Report) -> dict | None:
    url = str(entry.get("url") or "")
    title = str(entry.get("title") or url)
    if not url.startswith(("http://", "https://")):
        report.skipped.append(f"item {title}: url {url!r}")
        return None

    status = entry.get("statusCheck", default_status)
    config = {
        "url": url,
        "description": str(entry.get("description") or ""),
        "icon": _dashy_icon(str(entry.get("icon") or "")),
        "target": LinkTarget.SAME_TAB.value if entry.get("target") == "sametab" else LinkTarget.NEW_TAB.value,
        "status": "http" if status else "off",
        "status_url": str(entry.get("statusCheckUrl") or ""),
        "accept": [int(c) for c in str(entry.get("statusCheckAcceptCodes") or "").split(",") if c.strip().isdigit()],
        "insecure": bool(entry.get("statusCheckAllowInsecure", False)),
        "hotkey": str(entry.get("hotkey") or ""),
    }
    if entry.get("icon") and not config["icon"]:
        report.notes.append(f"{title}: icon {entry['icon']} → monogram")
    return {"type": "link", "title": title, "config": config}


def _dashy_widget(entry: dict, report: Report) -> dict | None:
    kind = str(entry.get("type") or "")
    options = entry.get("options") or {}
    if kind.startswith(DASHY_GLANCES):
        report.notes.append(f"widget {kind}: add a Glances connection, then a sysinfo widget")
        return None

    target = DASHY_WIDGETS.get(kind)
    if target is None:
        report.skipped.append(f"widget {kind}")
        return None

    title = str(entry.get("label") or options.get("label") or "")
    if target == "rss":
        return {"type": "rss", "title": title, "config": {"url": options.get("rssUrl", ""), "limit": int(options.get("limit", 8))}}
    if target == "clock":
        zone = options.get("timeZone")
        return {"type": "clock", "title": title, "config": {"timezones": [zone] if zone else ["Europe/Berlin"]}}
    if target == "weather":
        lat, lon = options.get("lat"), options.get("lon")
        if lat is None or lon is None:
            report.skipped.append(f"widget {kind}: needs lat/lon (city names are not resolved)")
            return None
        return {"type": "weather", "title": title, "config": {"lat": float(lat), "lon": float(lon)}}
    if target == "iframe":
        return {"type": "iframe", "title": title, "config": {"url": options.get("url", ""), "height": int(options.get("frameHeight", 320))}}
    return {"type": target, "title": title, "config": {}}


def import_dashy(who: Principal, space_id: int, text: str) -> Report:
    doc, report = dashy_to_doc(text)
    result = import_space(who, space_id, yaml.safe_dump(doc), ImportMode.MERGE)
    result.skipped = report.skipped + result.skipped
    result.notes = report.notes + result.notes
    return result
