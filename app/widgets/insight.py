"""Insight widgets: metrics, tables, charts, budgets, deadlines, hints.

All data comes from the <service>.data dataset of the widget's connection;
view functions turn it into template values with the pure metric modules.
"""

from datetime import date, timedelta
from enum import StrEnum

from pydantic import BaseModel, Field

from app.enums import Severity
from app.metrics import deadlines as dl
from app.metrics import kimai as km
from app.metrics import ninja as nm
from app.metrics import snipe as sm
from app.metrics import travel
from app.metrics.dates import add_months, month_start, parse_day
from app.widgets.base import Category, ConnUse, Extra, Query, ViewCtx, WidgetType, register

MINUTES_PER_HOUR = 60
DEFAULT_INCOME_TAX = 0.3


class Metric(StrEnum):
    HOURS_TODAY = "hours_today"
    HOURS_WEEK = "hours_week"
    HOURS_MONTH = "hours_month"
    UTILIZATION = "utilization"
    UNBILLED = "unbilled"
    REVENUE_YTD = "revenue_ytd"
    REVENUE_MONTH = "revenue_month"
    OPEN_AMOUNT = "open_amount"
    OVERDUE_AMOUNT = "overdue_amount"
    VAT_LIABILITY = "vat_liability"
    TAX_RESERVE = "tax_reserve"
    ASSET_VALUE = "asset_value"
    ASSETS_READY = "assets_ready"
    REVENUE_FORECAST = "revenue_forecast"
    CASH_30 = "cash_30"


class TableKind(StrEnum):
    OPEN_INVOICES = "open_invoices"
    UNBILLED = "unbilled"
    BUDGETS = "budgets"
    CLIENT_SHARES = "client_shares"
    ASSET_DATES = "asset_dates"
    TRIPS = "trips"


class ChartKind(StrEnum):
    REVENUE = "revenue"
    HOURS = "hours"


class KpiConfig(BaseModel):
    metric: Metric = Metric.REVENUE_YTD


class TableConfig(BaseModel):
    table: TableKind = TableKind.OPEN_INVOICES
    limit: int = Field(default=8, ge=1, le=50)


class ChartConfig(BaseModel):
    chart: ChartKind = ChartKind.REVENUE
    months: int = Field(default=12, ge=3, le=24)


class ProgressConfig(BaseModel):
    goal: bool = True


class HintsConfig(BaseModel):
    sources: list[str] = Field(default_factory=list)
    min_severity: int = Field(default=int(Severity.INFO), ge=10, le=30)
    limit: int = Field(default=8, ge=1, le=50)


class TrendMetric(StrEnum):
    REVENUE_YTD = "revenue_ytd"
    OPEN_AMOUNT = "open_amount"
    MONTH_MIN = "month_min"


class TrendConfig(BaseModel):
    metric: TrendMetric = TrendMetric.OPEN_AMOUNT
    days: int = Field(default=90, ge=7, le=730)


class DeadlinesConfig(BaseModel):
    days: int = Field(default=45, ge=7, le=400)


def _n(value: float, digits: int = 0) -> dict:
    return {"$num": value, "digits": digits}


def _data(slots: dict) -> dict:
    return slots.get("data") or {}


def _tax(ctx: ViewCtx) -> tuple[str, str]:
    vat = ((ctx.settings.get("tax") or {}).get("vat")) or {}
    return vat.get("return_interval", "monthly"), vat.get("method", "ist")


# ── KPI ──


def _kpi_kimai(metric: Metric, data: dict, ctx: ViewCtx) -> dict | None:
    hours_per_day = float((ctx.settings.get("goals") or {}).get("hours_per_day") or km.DEFAULT_HOURS_PER_DAY)
    stats = km.summary(data, ctx.today, hours_per_day)
    minutes = {Metric.HOURS_TODAY: stats["today_min"], Metric.HOURS_WEEK: stats["week_min"],
               Metric.HOURS_MONTH: stats["month_min"]}
    if metric in minutes:
        return {"kind": "hours", "value": minutes[metric] / MINUTES_PER_HOUR}
    if metric == Metric.UTILIZATION and stats["utilization"] is not None:
        return {"kind": "percent", "value": stats["utilization"],
                "sub": {"key": "kpi.of_target", "hours": _n(stats["target_month_min"] / MINUTES_PER_HOUR)}}
    if metric == Metric.UNBILLED:
        amount = sum(g["amount"] for g in stats["unbilled"])
        hours = sum(g["minutes"] for g in stats["unbilled"]) / MINUTES_PER_HOUR
        return {"kind": "money", "value": amount, "sub": {"key": "kpi.hours", "hours": _n(hours, 1)}}
    return None


def _kpi_ninja(metric: Metric, data: dict, ctx: ViewCtx) -> dict | None:
    interval, method = _tax(ctx)
    stats = nm.summary(data, ctx.today, interval, method)
    currency = stats["currency"]
    if metric == Metric.REVENUE_YTD:
        prev = stats["revenue_prev_ytd"]
        delta = (stats["revenue_ytd"] - prev) / prev if prev else None
        return {"kind": "money", "value": stats["revenue_ytd"], "currency": currency, "delta": delta}
    if metric == Metric.REVENUE_MONTH:
        return {"kind": "money", "value": stats["revenue_month"], "currency": currency}
    if metric == Metric.OPEN_AMOUNT:
        return {"kind": "money", "value": stats["open_amount"], "currency": currency,
                "sub": {"key": "kpi.invoices", "count": len(stats["open"])}}
    if metric == Metric.OVERDUE_AMOUNT:
        return {"kind": "money", "value": sum(i["balance"] for i in stats["overdue"]), "currency": currency,
                "sub": {"key": "kpi.invoices", "count": len(stats["overdue"])}}
    if metric == Metric.VAT_LIABILITY:
        vat = stats["vat"]
        return {"kind": "money", "value": vat["liability"], "currency": currency,
                "sub": {"key": "kpi.vat_period", "start": {"$day": vat["start"]}, "end": {"$day": vat["end"]}}}
    if metric == Metric.REVENUE_FORECAST:
        forecast = nm.forecast_year(data, ctx.today)
        goal = float((ctx.settings.get("goals") or {}).get("revenue_year") or 0)
        sub = {"key": "kpi.of_goal", "goal": {"$money": goal, "currency": currency}} if goal else None
        return {"kind": "money", "value": forecast, "currency": currency, "sub": sub}
    if metric == Metric.CASH_30:
        return {"kind": "money", "value": nm.cash_expected(data, ctx.today, 30), "currency": currency,
                "sub": {"key": "kpi.cash_30"}}
    if metric == Metric.TAX_RESERVE:
        rate = float((ctx.settings.get("tax") or {}).get("income_tax_rate") or DEFAULT_INCOME_TAX)
        expenses = sum(e["amount"] - e.get("tax", 0) for e in data.get("expenses", [])
                       if (d := parse_day(e.get("date"))) and d.year == ctx.today.year)
        surplus = max(stats["revenue_ytd"] - expenses, 0)
        return {"kind": "money", "value": stats["vat"]["liability"] + surplus * rate, "currency": currency,
                "sub": {"key": "kpi.reserve", "rate": round(rate * 100)}}
    return None


def _kpi_snipe(metric: Metric, data: dict, ctx: ViewCtx) -> dict | None:
    stats = sm.summary(data, ctx.today)
    if metric == Metric.ASSET_VALUE:
        return {"kind": "money", "value": stats["value"], "sub": {"key": "kpi.assets", "count": stats["assets"]}}
    if metric == Metric.ASSETS_READY:
        return {"kind": "count", "value": stats["ready"]}
    return None


def _kpi_view(cfg: KpiConfig, slots: dict, ctx: ViewCtx) -> dict:
    data = _data(slots)
    if not data:
        return {}
    builder = {"kimai": _kpi_kimai, "invoiceninja": _kpi_ninja, "snipeit": _kpi_snipe}.get(ctx.service or "")
    value = builder(cfg.metric, data, ctx) if builder else None
    return {"kpi": value, "unsupported": value is None}


# ── Tables ──


def _table_view(cfg: TableConfig, slots: dict, ctx: ViewCtx) -> dict:
    data = _data(slots)
    if not data:
        return {}
    rows = _rows(cfg.table, data, ctx)
    return {"rows": rows[: cfg.limit] if rows is not None else None, "total": len(rows or [])}


def _rows(kind: TableKind, data: dict, ctx: ViewCtx) -> list[dict] | None:
    service = ctx.service
    if kind == TableKind.OPEN_INVOICES and service == "invoiceninja":
        return [{"a": i["number"] or "–", "b": i["client"], "due": i["due_date"], "money": i["balance"],
                 "late": i["overdue_days"]} for i in nm.open_invoices(data, ctx.today)]
    if kind == TableKind.CLIENT_SHARES and service == "invoiceninja":
        return [{"a": s["client"], "money": s["net"], "pct": s["share"]} for s in nm.shares(data, ctx.today)]
    if kind == TableKind.UNBILLED and service == "kimai":
        return [{"a": g["customer"], "hours": g["minutes"] / MINUTES_PER_HOUR, "money": g["amount"],
                 "due": g["oldest"]} for g in km.unbilled(data, ctx.today)]
    if kind == TableKind.BUDGETS and service == "kimai":
        return _budgets(data, ctx)
    if kind == TableKind.ASSET_DATES and service == "snipeit":
        return [{"a": i["name"], "b": i["kind"], "due": i["date"]} for i in sm.upcoming(data, ctx.today)]
    if kind == TableKind.TRIPS and service == "dawarich":
        start = add_months(ctx.today, -1)
        mapping = ctx.options.get("areas") or {}
        return [{"a": t["area"], "due": t["day"], "km": t["km"], "hours": t["away_min"] / MINUTES_PER_HOUR}
                for t in travel.trips(data, mapping, start, ctx.today)]
    return None


def _budgets(data: dict, ctx: ViewCtx) -> list[dict]:
    rows = []
    for project in data.get("projects", []):
        if project.get("budget"):
            used = project.get("used_money", 0)
            rows.append({"a": project["name"], "pct": used / project["budget"], "money": used,
                         "limit": project["budget"]})
        elif project.get("time_budget_min"):
            used = project.get("used_minutes", 0)
            if project.get("budget_type") == "month":
                start = month_start(ctx.today)
                used = sum(s["minutes"] for s in data.get("timesheets", [])
                           if s.get("project_id") == project["id"] and (d := parse_day(s.get("begin"))) and d >= start)
            rows.append({"a": project["name"], "pct": used / project["time_budget_min"],
                         "hours": used / MINUTES_PER_HOUR})
    return sorted(rows, key=lambda r: -r["pct"])


# ── Chart ──


def _chart_view(cfg: ChartConfig, slots: dict, ctx: ViewCtx) -> dict:
    data = _data(slots)
    if not data:
        return {}
    if cfg.chart == ChartKind.REVENUE and ctx.service == "invoiceninja":
        bars = [{"label": b["month"], "value": b["net"], "prev": b["prev"]}
                for b in nm.by_month(data, ctx.today, cfg.months)]
        return {"bars": bars, "unit": "money"}
    if cfg.chart == ChartKind.HOURS and ctx.service == "kimai":
        bars = []
        for back in range(cfg.months - 1, -1, -1):
            start = add_months(ctx.today, -back)
            end = add_months(start, 1) - timedelta(days=1)
            prev = date(start.year - 1, start.month, 1)
            prev_end = add_months(prev, 1) - timedelta(days=1)
            bars.append({"label": start.isoformat()[:7],
                         "value": km.minutes_between(data, start, end) / MINUTES_PER_HOUR,
                         "prev": km.minutes_between(data, prev, prev_end) / MINUTES_PER_HOUR})
        return {"bars": bars, "unit": "hours"}
    return {"bars": None}


# ── Progress (budgets + revenue goal) ──


def _progress_view(cfg: ProgressConfig, slots: dict, ctx: ViewCtx) -> dict:
    data = _data(slots)
    if not data:
        return {}
    items = []
    if ctx.service == "kimai":
        items = [{"label": r["a"], "pct": r["pct"]} for r in _budgets(data, ctx)]
    goal = float((ctx.settings.get("goals") or {}).get("revenue_year") or 0)
    if ctx.service == "invoiceninja" and cfg.goal and goal:
        ytd = nm.summary(data, ctx.today)["revenue_ytd"]
        items.append({"label_key": "progress.revenue_goal", "pct": ytd / goal, "value": ytd, "goal": goal})
    return {"items": items}


# ── Deadlines ──


def _deadline_view(cfg: DeadlinesConfig, slots: dict, ctx: ViewCtx) -> dict:
    items = dl.upcoming(ctx.settings, ctx.today, cfg.days)
    return {"items": [{**i, "due": i["due"].isoformat(), "left": (i["due"] - ctx.today).days} for i in items],
            "configured": bool(ctx.settings.get("tax"))}


TREND_W = 1000
TREND_H = 160


def _trend_view(cfg: TrendConfig, slots: dict, ctx: ViewCtx) -> dict:
    """SVG path from daily snapshots; the service puts them into slots["points"]."""
    points = slots.get("points") or []
    if len(points) < 2:
        return {"path": None, "count": len(points)}
    values = [v for _d, v in points]
    if cfg.metric == TrendMetric.MONTH_MIN:
        values = [v / MINUTES_PER_HOUR for v in values]
    low, high = min(values), max(values)
    span = (high - low) or 1
    step = TREND_W / (len(values) - 1)
    coords = [f"{i * step:.1f},{TREND_H - (v - low) / span * (TREND_H - 10) - 5:.1f}" for i, v in enumerate(values)]
    return {"path": "M" + " L".join(coords), "first": points[0][0], "last": points[-1][0], "low": low, "high": high,
            "now": values[-1], "unit": "hours" if cfg.metric == TrendMetric.MONTH_MIN else "money",
            "w": TREND_W, "h": TREND_H}


def _data_query(_cfg) -> list[Query]:
    return [Query("data", "data", {}, ConnUse.WIDGET)]


register(WidgetType("kpi", KpiConfig, "widgets/kpi.html", Category.INSIGHT, refresh_s=600,
                    queries=_data_query, view=_kpi_view))
register(WidgetType("table", TableConfig, "widgets/table.html", Category.INSIGHT, refresh_s=600,
                    queries=_data_query, view=_table_view))
register(WidgetType("chart", ChartConfig, "widgets/chart.html", Category.INSIGHT, refresh_s=3600,
                    queries=_data_query, view=_chart_view))
register(WidgetType("progress", ProgressConfig, "widgets/progress.html", Category.INSIGHT, refresh_s=600,
                    queries=_data_query, view=_progress_view))
register(WidgetType("deadlines", DeadlinesConfig, "widgets/deadlines.html", Category.INSIGHT,
                    refresh_s=3600, view=_deadline_view))
register(WidgetType("trend", TrendConfig, "widgets/trend.html", Category.INSIGHT, refresh_s=3600,
                    view=_trend_view, extra=Extra.POINTS))
register(WidgetType("hints", HintsConfig, "widgets/hints.html", Category.INSIGHT, refresh_s=300,
                    extra=Extra.HINTS))
