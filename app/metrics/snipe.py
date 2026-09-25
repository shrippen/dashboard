"""Snipe-IT metrics: inventory value and upcoming ends."""

from datetime import date

from app.metrics.dates import parse_day

HORIZON_DAYS = 90


def upcoming(data: dict, today: date, days: int = HORIZON_DAYS) -> list[dict]:
    """Warranty, licence and EOL dates within `days`, sorted by date."""
    items = []
    for asset in data.get("assets", []):
        for kind, field_name in (("warranty", "warranty_expires"), ("eol", "eol_date")):
            when = parse_day(asset.get(field_name))
            if when and 0 <= (when - today).days <= days:
                items.append({"kind": kind, "name": asset["name"], "date": when.isoformat()})
    for lic in data.get("licenses", []):
        when = parse_day(lic.get("expires"))
        if when and 0 <= (when - today).days <= days:
            items.append({"kind": "license", "name": lic["name"], "date": when.isoformat()})
    return sorted(items, key=lambda i: i["date"])


def summary(data: dict, today: date) -> dict:
    assets = data.get("assets", [])
    return {
        "assets": len(assets),
        "value": round(sum(a.get("purchase_cost", 0) for a in assets), 2),
        "ready": sum(1 for a in assets if a.get("deployable") and not a.get("assigned")),
        "upcoming": upcoming(data, today),
        "audit_overdue": len(data.get("audit_overdue", [])),
    }
