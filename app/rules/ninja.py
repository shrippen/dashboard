"""Invoice Ninja rules: receivables, quotes, VAT, client concentration."""

from datetime import timedelta

from app.enums import ServiceType, Severity
from app.metrics import ninja as nm
from app.metrics.dates import parse_day
from app.rules.base import Env, Finding, day, money, num, rule

SOURCE = ServiceType.INVOICENINJA.value
OPEN = "open_in_invoiceninja"
GOAL_FROM_MONTH = 3
DAYS_PER_YEAR = 365
RECENT_DAYS = 90


def _url(data: dict, path: str) -> str:
    return f"{data.get('url', '').rstrip('/')}/#/{path}"


def _cur(data: dict) -> str:
    return data.get("currency", "EUR")


def _clients(data: dict) -> dict:
    return {c["id"]: c for c in data.get("clients", [])}


@rule("in.invoice_overdue", ServiceType.INVOICENINJA, dunning_after_days=14)
def overdue(data: dict, cfg: dict, env: Env) -> list[Finding]:
    found = []
    for invoice in nm.open_invoices(data, env.today):
        if invoice["overdue_days"] <= 0:
            continue
        dunning = invoice["overdue_days"] >= cfg["dunning_after_days"]
        found.append(Finding(
            f"overdue:{invoice['id']}", "in.invoice_overdue",
            Severity.CRITICAL if dunning else Severity.WARN,
            "in.overdue_dunning" if dunning else "in.overdue",
            {"number": invoice["number"], "client": invoice["client"], "days": invoice["overdue_days"],
             "amount": money(invoice["balance"], _cur(data))},
            _url(data, f"invoices/{invoice['id']}/edit"), OPEN, sources=[SOURCE]))
    return found


@rule("in.slow_payer", ServiceType.INVOICENINJA, days=30)
def slow_payer(data: dict, cfg: dict, env: Env) -> list[Finding]:
    clients = _clients(data)
    return [
        Finding(f"slow:{cid}", "in.slow_payer", Severity.INFO, "in.slow_payer",
                {"client": clients.get(cid, {}).get("name", "?"), "days": avg, "limit": cfg["days"]},
                sources=[SOURCE])
        for cid, avg in nm.payment_days(data).items() if avg > cfg["days"]
    ]


@rule("in.draft_stale", ServiceType.INVOICENINJA, days=7)
def draft_stale(data: dict, cfg: dict, env: Env) -> list[Finding]:
    clients = _clients(data)
    found = []
    for invoice in data.get("invoices", []):
        created = parse_day(invoice.get("date"))
        if invoice.get("status") != "draft" or not created or (env.today - created).days < cfg["days"]:
            continue
        found.append(Finding(
            f"draft:{invoice['id']}", "in.draft_stale", Severity.INFO, "in.draft_stale",
            {"client": clients.get(invoice.get("client_id"), {}).get("name", "?"),
             "days": (env.today - created).days, "amount": money(invoice["amount"], _cur(data))},
            _url(data, f"invoices/{invoice['id']}/edit"), OPEN, sources=[SOURCE]))
    return found


@rule("in.quote_open", ServiceType.INVOICENINJA, days=14)
def quote_open(data: dict, cfg: dict, env: Env) -> list[Finding]:
    clients = _clients(data)
    found = []
    for quote in data.get("quotes", []):
        sent = parse_day(quote.get("date"))
        if quote.get("status") != "sent" or not sent or (env.today - sent).days < cfg["days"]:
            continue
        found.append(Finding(
            f"quote:{quote['id']}", "in.quote_open", Severity.INFO, "in.quote_open",
            {"number": quote["number"], "client": clients.get(quote.get("client_id"), {}).get("name", "?"),
             "days": (env.today - sent).days}, _url(data, f"quotes/{quote['id']}/edit"), OPEN, sources=[SOURCE]))
    return found


@rule("in.recurring_ending", ServiceType.INVOICENINJA, days=30)
def recurring_ending(data: dict, cfg: dict, env: Env) -> list[Finding]:
    clients = _clients(data)
    found = []
    for item in data.get("recurring", []):
        cycles = item.get("remaining_cycles")
        send = parse_day(item.get("next_send_date"))
        if not item.get("active") or cycles in (None, -1) or cycles > 1 or not send:
            continue
        if (send - env.today).days > cfg["days"]:
            continue
        found.append(Finding(
            f"recurring:{item['id']}", "in.recurring_ending", Severity.INFO, "in.recurring_ending",
            {"client": clients.get(item.get("client_id"), {}).get("name", "?"), "day": day(send)},
            _url(data, f"recurring_invoices/{item['id']}/edit"), OPEN, day(send)["$day"], [SOURCE]))
    return found


@rule("in.revenue_vs_goal", ServiceType.INVOICENINJA)
def revenue_goal(data: dict, cfg: dict, env: Env) -> list[Finding]:
    goal = float((env.settings.get("goals") or {}).get("revenue_year") or 0)
    if not goal or env.today.month < GOAL_FROM_MONTH:
        return []
    summary = nm.summary(data, env.today)
    expected = goal * env.today.timetuple().tm_yday / DAYS_PER_YEAR
    if summary["revenue_ytd"] >= expected:
        return []
    return [Finding(
        f"goal:{env.today.year}", "in.revenue_vs_goal", Severity.INFO, "in.revenue_goal",
        {"ytd": money(summary["revenue_ytd"], _cur(data)), "expected": money(expected, _cur(data)),
         "goal": money(goal, _cur(data))}, sources=[SOURCE])]


@rule("in.missing_vat", ServiceType.INVOICENINJA, days=90)
def missing_vat(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Regularly taxed: domestic invoices need VAT; EU invoices at 0 % need the client's VAT id."""
    clients = _clients(data)
    home = str(data.get("home_country_id") or "276")
    found = []
    for invoice in nm.counted(data):
        if (env.today - parse_day(invoice["date"])).days > cfg["days"] or invoice["taxes"]:
            continue
        client = clients.get(invoice["client_id"], {})
        domestic = str(client.get("country_id") or home) == home
        if not domestic and client.get("vat_number"):
            continue
        found.append(Finding(
            f"vat:{invoice['id']}", "in.missing_vat", Severity.WARN,
            "in.missing_vat_domestic" if domestic else "in.missing_vat_eu",
            {"number": invoice["number"], "client": client.get("name", "?")},
            _url(data, f"invoices/{invoice['id']}/edit"), OPEN, sources=[SOURCE]))
    return found


@rule("in.expense_no_input_vat", ServiceType.INVOICENINJA, min_amount=50)
def expense_no_vat(data: dict, cfg: dict, env: Env) -> list[Finding]:
    since = env.today - timedelta(days=RECENT_DAYS)
    return [
        Finding(f"expense:{e['id']}", "in.expense_no_input_vat", Severity.INFO, "in.expense_no_vat",
                {"amount": money(e["amount"], _cur(data)), "notes": e.get("notes") or "–", "day": day(e["date"])},
                _url(data, f"expenses/{e['id']}/edit"), OPEN, sources=[SOURCE])
        for e in data.get("expenses", [])
        if not e.get("tax") and e.get("amount", 0) >= cfg["min_amount"]
        and (d := parse_day(e.get("date"))) and d >= since
    ]


@rule("in.client_concentration", ServiceType.INVOICENINJA, info_share=0.5, warn_share=5 / 6)
def concentration(data: dict, cfg: dict, env: Env) -> list[Finding]:
    """Klumpenrisiko; above 5/6 of revenue: check pension insurance duty (§ 2 Nr. 9 SGB VI)."""
    top = next(iter(nm.shares(data, env.today)), None)
    if top is None or top["share"] < cfg["info_share"]:
        return []
    warn = top["share"] >= cfg["warn_share"]
    return [Finding(
        f"concentration:{top['client_id']}", "in.client_concentration",
        Severity.WARN if warn else Severity.INFO,
        "in.concentration_pension" if warn else "in.concentration",
        {"client": top["client"], "percent": num(top["share"] * 100)}, sources=[SOURCE])]
