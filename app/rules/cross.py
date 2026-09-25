"""Rules across services of one space (and one credential owner).

    dawarich ↔ kimai          client visit without booking, booking "on site" without visit
    dawarich                  travel costs, per diem, tracking stopped
    snipeit ↔ invoiceninja    purchase without expense
"""

from datetime import UTC, datetime, timedelta

from app.enums import ServiceType, Severity
from app.metrics import travel
from app.metrics.dates import add_months, month_start, parse_day, parse_time
from app.rules import snipe
from app.rules.base import CROSS, Env, Finding, day, money, num, rule

KIMAI = ServiceType.KIMAI.value
NINJA = ServiceType.INVOICENINJA.value
SNIPE = ServiceType.SNIPEIT.value
DAWARICH = ServiceType.DAWARICH.value
REPORT_DAYS = 7
MINUTES_PER_HOUR = 60
LOOKBACK_DAYS = 30


def _mapping(env: Env) -> dict:
    return (env.options.get(DAWARICH) or {}).get("areas") or {}


def _booked(kimai: dict) -> set[tuple]:
    return {(parse_day(s.get("begin")), s.get("customer_id")) for s in kimai.get("timesheets", [])}


def _names(kimai: dict) -> dict:
    return {c["id"]: c["name"] for c in kimai.get("customers", [])}


@rule("geo.visit_without_time", CROSS, min_minutes=120)
def visit_without_time(data: dict, cfg: dict, env: Env) -> list[Finding]:
    kimai, geo = env.datasets.get(KIMAI), env.datasets.get(DAWARICH)
    if not kimai or not geo:
        return []

    booked, names = _booked(kimai), _names(kimai)
    since = env.today - timedelta(days=LOOKBACK_DAYS)
    found = []
    for visit in travel.client_visits(geo, _mapping(env)):
        if visit["day"] < since or visit["day"] >= env.today or visit["minutes"] < cfg["min_minutes"]:
            continue
        if (visit["day"], visit["customer_id"]) in booked:
            continue
        found.append(Finding(
            f"visit:{visit['day']}:{visit['customer_id']}", "geo.visit_without_time", Severity.WARN,
            "geo.visit_without_time",
            {"customer": names.get(visit["customer_id"], "?"), "day": day(visit["day"]),
             "hours": num(visit["minutes"] / MINUTES_PER_HOUR, 1)},
            f"{kimai.get('url', '').rstrip('/')}/timesheet/", "open_in_kimai", sources=[DAWARICH, KIMAI]))
    return found


@rule("geo.time_without_visit", CROSS, keywords=["vor ort", "on-site", "onsite"])
def time_without_visit(data: dict, cfg: dict, env: Env) -> list[Finding]:
    kimai, geo = env.datasets.get(KIMAI), env.datasets.get(DAWARICH)
    if not kimai or not geo:
        return []

    visited = {(v["day"], v["customer_id"]) for v in travel.client_visits(geo, _mapping(env))}
    mapped = {t.get("customer_id") for t in _mapping(env).values() if t.get("customer_id")}
    names = _names(kimai)
    since = env.today - timedelta(days=LOOKBACK_DAYS)
    words = [w.lower() for w in cfg["keywords"]]
    found = []
    for sheet in kimai.get("timesheets", []):
        when = parse_day(sheet.get("begin"))
        if not when or when < since or sheet.get("customer_id") not in mapped:
            continue
        if not any(w in (sheet.get("activity") or "").lower() for w in words):
            continue
        if (when, sheet["customer_id"]) in visited:
            continue
        found.append(Finding(
            f"novisit:{sheet['id']}", "geo.time_without_visit", Severity.INFO, "geo.time_without_visit",
            {"customer": names.get(sheet["customer_id"], "?"), "day": day(when)}, sources=[KIMAI, DAWARICH]))
    return found


def _last_month(env: Env):
    start = add_months(env.today, -1)
    return start, month_start(env.today) - timedelta(days=1)


@rule("geo.travel_costs", CROSS, km_rate=0.30)
def travel_costs(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """First week of a month: trips of last month × km rate."""
    geo = env.datasets.get(DAWARICH)
    if not geo or env.today.day > REPORT_DAYS:
        return []
    start, end = _last_month(env)
    trips = travel.trips(geo, _mapping(env), start, end)
    km = sum(t["km"] for t in trips)
    if not km:
        return []
    return [Finding(
        f"travel:{start.isoformat()[:7]}", "geo.travel_costs", Severity.INFO, "geo.travel_costs",
        {"trips": len(trips), "km": num(km), "amount": money(km * cfg["km_rate"]),
         "month": start.strftime("%m/%Y")}, sources=[DAWARICH])]


@rule("geo.per_diem", CROSS, over_8h=14.0, full_day=28.0)
def per_diem(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Days away from home longer than 8 h → Verpflegungsmehraufwand (rates configurable)."""
    geo = env.datasets.get(DAWARICH)
    if not geo or env.today.day > REPORT_DAYS:
        return []
    start, end = _last_month(env)
    days = [t for t in travel.trips(geo, _mapping(env), start, end) if t["away_min"] > 8 * MINUTES_PER_HOUR]
    if not days:
        return []
    return [Finding(
        f"perdiem:{start.isoformat()[:7]}", "geo.per_diem", Severity.INFO, "geo.per_diem",
        {"days": len(days), "amount": money(len(days) * cfg["over_8h"]), "month": start.strftime("%m/%Y")},
        sources=[DAWARICH])]


@rule("geo.no_data", ServiceType.DAWARICH, hours=24)
def no_data(data: dict, cfg: dict, env: Env) -> list[Finding]:
    last = parse_time(data.get("last_point"))
    if last is None:
        return []
    if last.tzinfo is None:
        last = last.replace(tzinfo=UTC)
    silent = (datetime.now(UTC) - last).total_seconds() / 3600
    if silent < cfg["hours"]:
        return []
    return [Finding("nodata", "geo.no_data", Severity.WARN, "geo.no_data", {"hours": num(silent)},
                    f"{data.get('url', '').rstrip('/')}/map", "open_in_dawarich", sources=[DAWARICH])]


@rule("snipe.expense_missing", CROSS, days=365, tolerance=0.05, date_window=14)
def expense_missing(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Asset bought this year without an expense of similar amount near the purchase date."""
    assets, ninja = env.datasets.get(SNIPE), env.datasets.get(NINJA)
    if not assets or not ninja:
        return []
    found = []
    for asset in snipe.recent_purchases(assets, env.today, cfg["days"]):
        bought = parse_day(asset["purchase_date"])
        if bought.year != env.today.year:
            continue
        cost = asset["purchase_cost"]
        match = [e for e in ninja.get("expenses", [])
                 if abs(e.get("amount", 0) - cost) <= cost * cfg["tolerance"]
                 or abs(e.get("amount", 0) - e.get("tax", 0) - cost) <= cost * cfg["tolerance"]]
        match = [e for e in match if (d := parse_day(e.get("date"))) and abs((d - bought).days) <= cfg["date_window"]]
        if match:
            continue
        found.append(Finding(
            f"expense:{asset['id']}", "snipe.expense_missing", Severity.INFO, "snipe.expense_missing",
            {"asset": asset["name"], "amount": money(cost), "day": day(bought)}, sources=[SNIPE, NINJA]))
    return found
