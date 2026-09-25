"""Kimai rules: timers, missing days, unbilled work, budgets, utilisation."""

from collections import defaultdict
from datetime import UTC, datetime, timedelta

from app.enums import ServiceType, Severity
from app.metrics import kimai as km
from app.metrics.dates import add_months, month_start, parse_day, week_start, workdays
from app.rules.base import Env, Finding, day, money, num, rule

SOURCE = ServiceType.KIMAI.value
OPEN = "open_in_kimai"
MINUTES_PER_HOUR = 60
MONTH_CLOSE_DAYS = 5
UTILIZATION_FROM_DAY = 10


def _url(data: dict, path: str) -> str:
    return f"{data.get('url', '').rstrip('/')}/{path}"


def _hours(minutes: float) -> dict:
    return num(minutes / MINUTES_PER_HOUR, 1)


@rule("kimai.timer_running_long", ServiceType.KIMAI, hours=10)
def timer_long(data: dict, cfg: dict, env: Env) -> list[Finding]:
    found = []
    for sheet in km.running(data, datetime.now(UTC)):
        if sheet["running_min"] < cfg["hours"] * MINUTES_PER_HOUR:
            continue
        found.append(Finding(
            f"timer:{sheet['id']}", "kimai.timer_running_long", Severity.WARN, "kimai.timer_long",
            {"hours": _hours(sheet["running_min"])}, _url(data, "timesheet/"), OPEN, sources=[SOURCE]))
    return found


@rule("kimai.missing_day", ServiceType.KIMAI, lookback_days=10)
def missing_day(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Workdays without any entry (holidays and absences excluded)."""
    booked = {parse_day(s.get("begin")) for s in data.get("timesheets", [])}
    start = env.today - timedelta(days=cfg["lookback_days"])
    days = workdays(start, env.today - timedelta(days=1), km.free_days(data))
    return [
        Finding(f"missing:{d.isoformat()}", "kimai.missing_day", Severity.INFO, "kimai.missing_day",
                {"day": day(d)}, _url(data, "timesheet/"), OPEN, sources=[SOURCE])
        for d in days if d not in booked
    ]


@rule("kimai.unbilled_hours", ServiceType.KIMAI, warn_days=30, critical_days=60)
def unbilled(data: dict, cfg: dict, env: Env) -> list[Finding]:
    found = []
    for group in km.unbilled(data, env.today):
        if group["age"] < cfg["warn_days"]:
            continue
        level = Severity.CRITICAL if group["age"] >= cfg["critical_days"] else Severity.WARN
        found.append(Finding(
            f"unbilled:{group['customer_id']}", "kimai.unbilled_hours", level, "kimai.unbilled",
            {"hours": _hours(group["minutes"]), "customer": group["customer"],
             "amount": money(group["amount"]), "oldest": day(group["oldest"]), "days": cfg["warn_days"]},
            _url(data, "abrechnung"), OPEN, sources=[SOURCE, ServiceType.INVOICENINJA.value]))
    return found


def _used(project: dict, data: dict, env: Env) -> tuple[float, float]:
    """Budget used (money, minutes); monthly budgets count the current month only."""
    if project.get("budget_type") != "month":
        return project.get("used_money", 0), project.get("used_minutes", 0)
    start = month_start(env.today)
    sheets = [s for s in data.get("timesheets", [])
              if s.get("project_id") == project["id"] and (d := parse_day(s.get("begin"))) and d >= start]
    return sum(s.get("rate", 0) for s in sheets), sum(s.get("minutes", 0) for s in sheets)


@rule("kimai.budget_burn", ServiceType.KIMAI, warn=0.8, critical=1.0)
def budget(data: dict, cfg: dict, env: Env) -> list[Finding]:
    found = []
    for project in data.get("projects", []):
        money_used, minutes_used = _used(project, data, env)
        ratios = []
        if project.get("budget"):
            ratios.append(money_used / project["budget"])
        if project.get("time_budget_min"):
            ratios.append(minutes_used / project["time_budget_min"])
        if not ratios or max(ratios) < cfg["warn"]:
            continue
        ratio = max(ratios)
        level = Severity.CRITICAL if ratio >= cfg["critical"] else Severity.WARN
        found.append(Finding(
            f"budget:{project['id']}", "kimai.budget_burn", level, "kimai.budget",
            {"project": project["name"], "percent": num(ratio * 100)},
            _url(data, f"admin/project/{project['id']}/details"), OPEN, sources=[SOURCE]))
    return found


@rule("kimai.budget_pace", ServiceType.KIMAI)
def budget_pace(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Money budget with an end date: will the current pace exceed it before the end?"""
    found = []
    for project in data.get("projects", []):
        end = parse_day(project.get("end"))
        if not project.get("budget") or not end or end <= env.today or project.get("budget_type"):
            continue
        recent = [s for s in data.get("timesheets", []) if s.get("project_id") == project["id"]
                  and (d := parse_day(s.get("begin"))) and d >= env.today - timedelta(days=30)]
        per_day = sum(s.get("rate", 0) for s in recent) / 30
        forecast = project.get("used_money", 0) + per_day * (end - env.today).days
        if forecast <= project["budget"]:
            continue
        found.append(Finding(
            f"pace:{project['id']}", "kimai.budget_pace", Severity.WARN, "kimai.pace",
            {"project": project["name"], "forecast": money(forecast), "budget": money(project["budget"]),
             "end": day(end)}, _url(data, f"admin/project/{project['id']}/details"), OPEN, sources=[SOURCE]))
    return found


@rule("kimai.utilization_low", ServiceType.KIMAI, goal=0.7, hours_per_day=8)
def utilization(data: dict, cfg: dict, env: Env) -> list[Finding]:
    if env.today.day < UTILIZATION_FROM_DAY:
        return []
    stats = km.summary(data, env.today, cfg["hours_per_day"])
    ratio = stats["utilization"]
    if ratio is None or ratio >= cfg["goal"]:
        return []
    return [Finding(
        f"utilization:{env.today.isoformat()[:7]}", "kimai.utilization_low", Severity.INFO,
        "kimai.utilization", {"percent": num(ratio * 100), "goal": num(cfg["goal"] * 100)},
        sources=[SOURCE])]


@rule("kimai.overtime", ServiceType.KIMAI, max_week_hours=45, weeks=2)
def overtime(data: dict, cfg: dict, env: Env) -> list[Finding]:
    per_week: dict = defaultdict(int)
    for sheet in data.get("timesheets", []):
        begin = parse_day(sheet.get("begin"))
        if begin:
            per_week[week_start(begin)] += sheet.get("minutes", 0)

    current = week_start(env.today)
    weeks = [current - timedelta(weeks=i + 1) for i in range(cfg["weeks"])]
    limit = cfg["max_week_hours"] * MINUTES_PER_HOUR
    if not all(per_week.get(w, 0) > limit for w in weeks):
        return []
    average = sum(per_week[w] for w in weeks) / len(weeks)
    return [Finding(
        f"overtime:{weeks[0].isoformat()}", "kimai.overtime", Severity.INFO, "kimai.overtime",
        {"hours": _hours(average), "weeks": cfg["weeks"], "limit": cfg["max_week_hours"]}, sources=[SOURCE])]


@rule("kimai.monthly_close", ServiceType.KIMAI)
def monthly_close(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """First days of a month: entries of last month not exported yet."""
    if env.today.day > MONTH_CLOSE_DAYS:
        return []
    start, end = add_months(env.today, -1), month_start(env.today) - timedelta(days=1)
    open_min = sum(s.get("minutes", 0) for s in data.get("timesheets", [])
                   if s.get("billable") and not s.get("exported")
                   and (d := parse_day(s.get("begin"))) and start <= d <= end)
    if not open_min:
        return []
    return [Finding(
        f"close:{start.isoformat()[:7]}", "kimai.monthly_close", Severity.WARN, "kimai.monthly_close",
        {"month": start.strftime("%m/%Y"), "hours": _hours(open_min)}, _url(data, "abrechnung"), OPEN,
        sources=[SOURCE])]
