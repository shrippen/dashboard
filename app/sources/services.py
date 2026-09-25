"""Service adapters: raw API data → normalised datasets.

Every service has two sources:

    <service>.test   version / reachability (connection test)
    <service>.data   one cached dataset that widgets, metrics and rules share

URLs starting with demo:// return generated demo data (see app/demo/data.py).
"""

from dataclasses import dataclass
from datetime import UTC, date, datetime, timedelta

from app.demo import data as demo
from app.drivers.services import ApiError, ApiMissing, DawarichApi, KimaiApi, NinjaApi, SnipeApi
from app.enums import ServiceType
from app.sources.base import Ctx, SourceError, register
from app.sources.web import Glances

DEMO_SCHEME = "demo://"
DATA_TTL = timedelta(minutes=10)
TEST_TTL = timedelta(seconds=1)
VISIT_DAYS = 120
SECONDS_PER_MINUTE = 60
NINJA_STATUS = {"1": "draft", "2": "sent", "3": "partial", "4": "paid", "5": "cancelled", "6": "reversed"}
QUOTE_STATUS = {"1": "draft", "2": "sent", "3": "approved", "4": "converted", "-1": "expired"}


def _is_demo(ctx: Ctx) -> bool:
    return ctx.url.startswith(DEMO_SCHEME)


def _need_secret(ctx: Ctx) -> str:
    if not ctx.secret:
        raise SourceError("credential.missing")
    return ctx.secret


def _window_start(today: date) -> date:
    """January 1st of last year: enough for year-over-year comparisons."""
    return date(today.year - 1, 1, 1)


def _day(value) -> str | None:
    if isinstance(value, dict):
        value = value.get("date") or value.get("datetime")
    if not value:
        return None
    return str(value)[:10]


def _num(value, default: float = 0.0) -> float:
    try:
        return float(value)
    except (TypeError, ValueError):
        return default


# ── Kimai ──


def _kimai(ctx: Ctx) -> KimaiApi:
    return KimaiApi(ctx.url, _need_secret(ctx), ctx.verify_tls)


def _ref(value) -> int | None:
    if isinstance(value, dict):
        return value.get("id")
    return value


def _sheet(raw: dict) -> dict:
    project = raw.get("project") if isinstance(raw.get("project"), dict) else {}
    customer = project.get("customer") if isinstance(project.get("customer"), dict) else {}
    activity = raw.get("activity") if isinstance(raw.get("activity"), dict) else {}
    return {
        "id": raw.get("id"),
        "begin": raw.get("begin"),
        "end": raw.get("end"),
        "minutes": round(_num(raw.get("duration")) / SECONDS_PER_MINUTE),
        "rate": _num(raw.get("rate")),
        "billable": bool(raw.get("billable", True)),
        "exported": bool(raw.get("exported", False)),
        "project_id": _ref(raw.get("project")),
        "customer_id": customer.get("id") or project.get("customer"),
        "activity": activity.get("name", ""),
        "user_id": _ref(raw.get("user")),
    }


def _project(raw: dict) -> dict:
    return {
        "id": raw.get("id"),
        "name": raw.get("name", ""),
        "customer_id": _ref(raw.get("customer")),
        "budget": _num(raw.get("budget")),
        "time_budget_min": round(_num(raw.get("timeBudget")) / SECONDS_PER_MINUTE),
        "budget_type": raw.get("budgetType"),
        "end": raw.get("end"),
        "used_money": 0.0,
        "used_minutes": 0,
    }


@dataclass(frozen=True)
class KimaiData:
    key: str = "kimai.data"
    ttl: timedelta = DATA_TTL
    service: ServiceType | None = ServiceType.KIMAI

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return demo.kimai(date.today())
        try:
            return self._load(ctx)
        except ApiError as exc:
            raise SourceError(str(exc)) from exc

    def _load(self, ctx: Ctx) -> dict:
        api = _kimai(ctx)
        today = date.today()
        scope = {"user": "all"} if ctx.options.get("all_users") else {}
        begin = _window_start(today).isoformat() + "T00:00:00"
        sheets = api.pages("timesheets", {**scope, "begin": begin, "full": "true"})
        projects = [_project(api.get(f"projects/{p['id']}")) for p in api.get("projects", {"visible": 3})]
        for project in projects:
            if project["budget"] or project["time_budget_min"]:
                used = api.pages("timesheets", {**scope, "projects[]": project["id"]})
                project["used_money"] = sum(_num(s.get("rate")) for s in used)
                project["used_minutes"] = round(sum(_num(s.get("duration")) for s in used) / 60)

        data = {
            "url": ctx.url,
            "timesheets": [_sheet(s) for s in sheets],
            "active": [_sheet(s) for s in api.get("timesheets/active") or []],
            "projects": projects,
            "customers": [{"id": c.get("id"), "name": c.get("name", "")}
                          for c in api.get("customers", {"visible": 3})],
            "absences": [],
            "holidays": [],
            "holiday_bundle": False,
        }
        self._holidays(api, today, data)
        return data

    def _holidays(self, api: KimaiApi, today: date, data: dict) -> None:
        """kimai-holiday-bundle: absences and public holidays; missing plugin is fine."""
        try:
            for year in (today.year - 1, today.year):
                data["absences"] += [
                    {"start": a.get("startDate"), "end": a.get("endDate"), "type": a.get("type"),
                     "status": a.get("status"), "half_day": bool(a.get("halfDay"))}
                    for a in api.get("holiday/absences", {"year": year}) or []
                ]
                data["holidays"] += [
                    {"date": h.get("date"), "name": h.get("name", ""), "half_day": bool(h.get("halfDay"))}
                    for h in api.get("holiday/public-holidays", {"year": year}) or []
                ]
            data["holiday_bundle"] = True
        except ApiMissing:
            return


@dataclass(frozen=True)
class KimaiTest:
    key: str = "kimai.test"
    ttl: timedelta = TEST_TTL
    service: ServiceType | None = ServiceType.KIMAI

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return {"version": "demo"}
        try:
            body = _kimai(ctx).get("version")
        except ApiError as exc:
            raise SourceError(str(exc)) from exc
        return {"version": body.get("version") if isinstance(body, dict) else None}


# ── Invoice Ninja ──


def _ninja(ctx: Ctx) -> NinjaApi:
    return NinjaApi(ctx.url, _need_secret(ctx), ctx.verify_tls)


def _invoice(raw: dict) -> dict:
    amount = _num(raw.get("amount"))
    taxes = _num(raw.get("total_taxes"))
    return {
        "id": raw.get("id"),
        "number": raw.get("number", ""),
        "client_id": raw.get("client_id"),
        "status": NINJA_STATUS.get(str(raw.get("status_id")), "draft"),
        "date": _day(raw.get("date")),
        "due_date": _day(raw.get("due_date")),
        "amount": amount,
        "balance": _num(raw.get("balance")),
        "taxes": taxes,
        "net": amount - taxes,
    }


def _expense_tax(raw: dict) -> float:
    """Input VAT of an expense: explicit tax amounts, else computed from the rate."""
    amounts = sum(_num(raw.get(f"tax_amount{i}")) for i in (1, 2, 3))
    if amounts:
        return amounts
    rate = sum(_num(raw.get(f"tax_rate{i}")) for i in (1, 2, 3))
    amount = _num(raw.get("amount"))
    if not rate:
        return 0.0
    if raw.get("uses_inclusive_taxes"):
        return amount * rate / (100 + rate)
    return amount * rate / 100


@dataclass(frozen=True)
class NinjaData:
    key: str = "invoiceninja.data"
    ttl: timedelta = DATA_TTL
    service: ServiceType | None = ServiceType.INVOICENINJA

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return demo.invoiceninja(date.today())
        try:
            return self._load(ctx)
        except ApiError as exc:
            raise SourceError(str(exc)) from exc

    def _load(self, ctx: Ctx) -> dict:
        api = _ninja(ctx)
        since = _window_start(date.today()).isoformat()
        invoices = [_invoice(i) for i in api.pages("invoices", {"is_deleted": "false"})
                    if (_day(i.get("date")) or "") >= since or _num(i.get("balance")) > 0]
        return {
            "url": ctx.url,
            "currency": ctx.options.get("currency", "EUR"),
            "invoices": invoices,
            "payments": [
                {"id": p.get("id"), "date": _day(p.get("date")), "amount": _num(p.get("amount")),
                 "client_id": p.get("client_id")}
                for p in api.pages("payments", {"include": "paymentables"})
                if (_day(p.get("date")) or "") >= since
            ],
            "clients": [
                {"id": c.get("id"), "name": c.get("display_name") or c.get("name", ""),
                 "vat_number": c.get("vat_number", ""), "country_id": str(c.get("country_id") or "")}
                for c in api.pages("clients")
            ],
            "expenses": [
                {"id": e.get("id"), "date": _day(e.get("date")), "amount": _num(e.get("amount")),
                 "tax": round(_expense_tax(e), 2), "notes": e.get("public_notes") or "",
                 "vendor_id": e.get("vendor_id")}
                for e in api.pages("expenses")
                if (_day(e.get("date")) or "") >= since
            ],
            "quotes": [
                {"id": q.get("id"), "number": q.get("number", ""), "client_id": q.get("client_id"),
                 "status": QUOTE_STATUS.get(str(q.get("status_id")), "draft"), "date": _day(q.get("date")),
                 "amount": _num(q.get("amount"))}
                for q in api.pages("quotes")
            ],
            "recurring": [
                {"id": r.get("id"), "number": r.get("number", ""), "client_id": r.get("client_id"),
                 "active": str(r.get("status_id")) == "2", "next_send_date": _day(r.get("next_send_date")),
                 "remaining_cycles": r.get("remaining_cycles"), "amount": _num(r.get("amount"))}
                for r in api.pages("recurring_invoices")
            ],
            "home_country_id": str(ctx.options.get("home_country_id", "276")),
        }


@dataclass(frozen=True)
class NinjaTest:
    key: str = "invoiceninja.test"
    ttl: timedelta = TEST_TTL
    service: ServiceType | None = ServiceType.INVOICENINJA

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return {"version": "demo"}
        try:
            return {"version": _ninja(ctx).version()}
        except ApiError as exc:
            raise SourceError(str(exc)) from exc


# ── Snipe-IT ──


def _snipe(ctx: Ctx) -> SnipeApi:
    return SnipeApi(ctx.url, _need_secret(ctx), ctx.verify_tls)


def _name(value) -> str:
    return value.get("name", "") if isinstance(value, dict) else str(value or "")


def _asset(raw: dict) -> dict:
    status = raw.get("status_label") or {}
    return {
        "id": raw.get("id"),
        "name": raw.get("name") or _name(raw.get("model")),
        "tag": raw.get("asset_tag", ""),
        "model": _name(raw.get("model")),
        "category": _name(raw.get("category")),
        "status": status.get("name", ""),
        "deployable": status.get("status_meta") == "deployable",
        "assigned": bool(raw.get("assigned_to")),
        "purchase_date": _day(raw.get("purchase_date")),
        "purchase_cost": _num(str(raw.get("purchase_cost") or "0").replace(",", "")),
        "warranty_expires": _day(raw.get("warranty_expires")),
        "eol_date": _day(raw.get("asset_eol_date")),
        "next_audit": _day(raw.get("next_audit_date")),
        "last_change": _day(raw.get("last_checkin") or raw.get("last_checkout") or raw.get("updated_at")),
    }


@dataclass(frozen=True)
class SnipeData:
    key: str = "snipeit.data"
    ttl: timedelta = DATA_TTL
    service: ServiceType | None = ServiceType.SNIPEIT

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return demo.snipeit(date.today())
        try:
            api = _snipe(ctx)
            return {
                "url": ctx.url,
                "assets": [_asset(a) for a in api.rows("hardware")],
                "licenses": [
                    {"id": lic.get("id"), "name": lic.get("name", ""),
                     "expires": _day(lic.get("expiration_date")) or _day(lic.get("termination_date")),
                     "seats": int(_num(lic.get("seats"))), "free": int(_num(lic.get("free_seats_count")))}
                    for lic in api.rows("licenses")
                ],
                "consumables": [
                    {"id": c.get("id"), "name": c.get("name", ""), "remaining": int(_num(c.get("remaining"))),
                     "min": int(_num(c.get("min_amt")))}
                    for c in api.rows("consumables")
                ],
                "audit_overdue": [a.get("id") for a in api.rows("hardware/audit/overdue")],
            }
        except ApiError as exc:
            raise SourceError(str(exc)) from exc


@dataclass(frozen=True)
class SnipeTest:
    key: str = "snipeit.test"
    ttl: timedelta = TEST_TTL
    service: ServiceType | None = ServiceType.SNIPEIT

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return {"version": "demo"}
        try:
            me = _snipe(ctx).get("users/me")
        except ApiError as exc:
            raise SourceError(str(exc)) from exc
        return {"version": None, "user": me.get("username") if isinstance(me, dict) else None}


# ── Dawarich ──


def _dawarich(ctx: Ctx) -> DawarichApi:
    return DawarichApi(ctx.url, _need_secret(ctx), ctx.verify_tls)


def _visit(raw: dict) -> dict:
    place = raw.get("place") or {}
    area = raw.get("area") or {}
    return {
        "id": raw.get("id"),
        "start": raw.get("started_at"),
        "end": raw.get("ended_at"),
        "minutes": int(_num(raw.get("duration"))),
        "area_id": raw.get("area_id") or area.get("id"),
        "name": raw.get("name") or place.get("name") or area.get("name", ""),
        "lat": _num(place.get("latitude"), None),
        "lon": _num(place.get("longitude"), None),
    }


@dataclass(frozen=True)
class DawarichData:
    key: str = "dawarich.data"
    ttl: timedelta = DATA_TTL
    service: ServiceType | None = ServiceType.DAWARICH

    def fetch(self, ctx: Ctx) -> dict:
        """Aggregates only: areas, visits, monthly distances, time of the last point."""
        if _is_demo(ctx):
            return demo.dawarich(date.today())
        try:
            api = _dawarich(ctx)
            now = datetime.now(UTC)
            window = {"start_at": (now - timedelta(days=VISIT_DAYS)).isoformat(), "end_at": now.isoformat()}
            last = api.get("points", {"per_page": 1, "order": "desc"}) or []
            return {
                "url": ctx.url,
                "areas": [
                    {"id": a.get("id"), "name": a.get("name", ""), "lat": _num(a.get("latitude")),
                     "lon": _num(a.get("longitude")), "radius": _num(a.get("radius"))}
                    for a in api.get("areas") or []
                ],
                "visits": [_visit(v) for v in api.get("visits", window) or []],
                "stats": api.get("stats") or {},
                "last_point": _last_point(last),
            }
        except ApiError as exc:
            raise SourceError(str(exc)) from exc


def _last_point(points) -> str | None:
    if not points:
        return None
    first = points[0]
    stamp = first.get("timestamp")
    if isinstance(stamp, int | float):
        return datetime.fromtimestamp(stamp, UTC).isoformat()
    return first.get("created_at") or None


@dataclass(frozen=True)
class DawarichTest:
    key: str = "dawarich.test"
    ttl: timedelta = TEST_TTL
    service: ServiceType | None = ServiceType.DAWARICH

    def fetch(self, ctx: Ctx) -> dict:
        if _is_demo(ctx):
            return {"version": "demo"}
        try:
            return {"version": _dawarich(ctx).version()}
        except ApiError as exc:
            raise SourceError(str(exc)) from exc


@dataclass(frozen=True)
class GlancesTest:
    key: str = "glances.test"
    ttl: timedelta = TEST_TTL
    service: ServiceType | None = ServiceType.GLANCES

    def fetch(self, ctx: Ctx) -> dict:
        Glances().fetch(ctx)
        return {"version": None}


for _source in (KimaiData(), KimaiTest(), NinjaData(), NinjaTest(), SnipeData(), SnipeTest(),
                DawarichData(), DawarichTest(), GlancesTest()):
    register(_source)
