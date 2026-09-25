"""Snipe-IT rules: warranty, end of life, licences, audits, stock, unused devices."""

from datetime import timedelta

from app.enums import ServiceType, Severity
from app.metrics.dates import parse_day
from app.rules.base import Env, Finding, day, money, rule

SOURCE = ServiceType.SNIPEIT.value
OPEN = "open_in_snipeit"
GWG_LIMIT_NET = 800.0
VAT_FACTOR = 1.19


def _url(data: dict, path: str) -> str:
    return f"{data.get('url', '').rstrip('/')}/{path}"


@rule("snipe.warranty_expiring", ServiceType.SNIPEIT, info_days=60, warn_days=14)
def warranty(data: dict, cfg: dict, env: Env) -> list[Finding]:
    found = []
    for asset in data.get("assets", []):
        ends = parse_day(asset.get("warranty_expires"))
        if not ends or not 0 <= (ends - env.today).days <= cfg["info_days"]:
            continue
        level = Severity.WARN if (ends - env.today).days <= cfg["warn_days"] else Severity.INFO
        found.append(Finding(
            f"warranty:{asset['id']}", "snipe.warranty_expiring", level, "snipe.warranty",
            {"asset": asset["name"], "tag": asset["tag"], "day": day(ends)},
            _url(data, f"hardware/{asset['id']}"), OPEN, ends.isoformat(), [SOURCE]))
    return found


@rule("snipe.eol_reached", ServiceType.SNIPEIT)
def eol(data: dict, cfg: dict, env: Env) -> list[Finding]:
    return [
        Finding(f"eol:{a['id']}", "snipe.eol_reached", Severity.INFO, "snipe.eol",
                {"asset": a["name"], "tag": a["tag"], "day": day(a["eol_date"])},
                _url(data, f"hardware/{a['id']}"), OPEN, sources=[SOURCE])
        for a in data.get("assets", [])
        if (d := parse_day(a.get("eol_date"))) and d <= env.today
    ]


@rule("snipe.license_expiring", ServiceType.SNIPEIT, days=30)
def license_expiring(data: dict, cfg: dict, env: Env) -> list[Finding]:
    return [
        Finding(f"license:{lic['id']}", "snipe.license_expiring", Severity.WARN, "snipe.license",
                {"license": lic["name"], "day": day(lic["expires"])},
                _url(data, f"licenses/{lic['id']}"), OPEN, lic["expires"], [SOURCE])
        for lic in data.get("licenses", [])
        if (d := parse_day(lic.get("expires"))) and 0 <= (d - env.today).days <= cfg["days"]
    ]


@rule("snipe.license_seats", ServiceType.SNIPEIT)
def license_seats(data: dict, cfg: dict, env: Env) -> list[Finding]:
    return [
        Finding(f"seats:{lic['id']}", "snipe.license_seats", Severity.INFO, "snipe.seats",
                {"license": lic["name"], "seats": lic["seats"]},
                _url(data, f"licenses/{lic['id']}"), OPEN, sources=[SOURCE])
        for lic in data.get("licenses", []) if lic.get("seats") and lic.get("free") == 0
    ]


@rule("snipe.audit_overdue", ServiceType.SNIPEIT)
def audit(data: dict, cfg: dict, env: Env) -> list[Finding]:
    names = {a["id"]: a for a in data.get("assets", [])}
    return [
        Finding(f"audit:{aid}", "snipe.audit_overdue", Severity.WARN, "snipe.audit",
                {"asset": names.get(aid, {}).get("name", f"#{aid}")},
                _url(data, f"hardware/{aid}"), OPEN, sources=[SOURCE])
        for aid in data.get("audit_overdue", [])
    ]


@rule("snipe.consumable_low", ServiceType.SNIPEIT)
def consumable(data: dict, cfg: dict, env: Env) -> list[Finding]:
    return [
        Finding(f"stock:{c['id']}", "snipe.consumable_low", Severity.INFO, "snipe.stock",
                {"item": c["name"], "left": c["remaining"], "min": c["min"]},
                _url(data, f"consumables/{c['id']}"), OPEN, sources=[SOURCE])
        for c in data.get("consumables", []) if c.get("min") and c.get("remaining", 0) < c["min"]
    ]


@rule("snipe.unassigned_deployable", ServiceType.SNIPEIT, days=90)
def unused(data: dict, cfg: dict, env: Env) -> list[Finding]:
    return [
        Finding(f"unused:{a['id']}", "snipe.unassigned_deployable", Severity.INFO, "snipe.unused",
                {"asset": a["name"], "days": (env.today - parse_day(a["last_change"])).days},
                _url(data, f"hardware/{a['id']}"), OPEN, sources=[SOURCE])
        for a in data.get("assets", [])
        if a.get("deployable") and not a.get("assigned") and (d := parse_day(a.get("last_change")))
        and (env.today - d).days >= cfg["days"]
    ]


@rule("snipe.gwg_hint", ServiceType.SNIPEIT, cost_is_gross=True)
def gwg(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Purchases this year above 800 € net: depreciation instead of immediate write-off."""
    found = []
    for asset in data.get("assets", []):
        bought = parse_day(asset.get("purchase_date"))
        if not bought or bought.year != env.today.year:
            continue
        net = asset.get("purchase_cost", 0) / (VAT_FACTOR if cfg["cost_is_gross"] else 1)
        if net <= GWG_LIMIT_NET:
            continue
        found.append(Finding(
            f"gwg:{asset['id']}", "snipe.gwg_hint", Severity.INFO, "snipe.gwg",
            {"asset": asset["name"], "net": money(net)}, _url(data, f"hardware/{asset['id']}"), OPEN,
            sources=[SOURCE]))
    return found


def recent_purchases(data: dict, today, days: int) -> list[dict]:
    since = today - timedelta(days=days)
    return [a for a in data.get("assets", [])
            if (d := parse_day(a.get("purchase_date"))) and d >= since and a.get("purchase_cost")]
