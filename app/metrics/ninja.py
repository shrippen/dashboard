"""Invoice Ninja metrics: revenue (net), receivables, VAT, payment behaviour."""

from collections import defaultdict
from datetime import date, timedelta

from app.metrics.dates import add_months, month_start, parse_day, quarter_start

COUNTED = ("sent", "partial", "paid")
OPEN = ("sent", "partial")
MONTHS_BACK = 12


def counted(data: dict) -> list[dict]:
    return [i for i in data.get("invoices", []) if i.get("status") in COUNTED and i.get("date")]


def revenue(data: dict, start: date, end: date) -> float:
    """Net revenue by invoice date: VAT is not revenue."""
    return round(sum(i["net"] for i in counted(data) if start <= parse_day(i["date"]) <= end), 2)


def by_month(data: dict, today: date, months: int = MONTHS_BACK) -> list[dict]:
    """[{"month": "2026-09", "net": 5200.0, "prev": 4100.0}] for the chart widget."""
    result = []
    for back in range(months - 1, -1, -1):
        start = add_months(today, -back)
        end = add_months(start, 1) - timedelta(days=1)
        prev_start = date(start.year - 1, start.month, 1)
        prev_end = add_months(prev_start, 1) - timedelta(days=1)
        result.append({"month": start.isoformat()[:7], "net": revenue(data, start, end),
                       "prev": revenue(data, prev_start, prev_end)})
    return result


def open_invoices(data: dict, today: date) -> list[dict]:
    clients = {c["id"]: c["name"] for c in data.get("clients", [])}
    result = []
    for invoice in data.get("invoices", []):
        if invoice.get("status") not in OPEN or invoice.get("balance", 0) <= 0:
            continue
        due = parse_day(invoice.get("due_date"))
        overdue = (today - due).days if due and due < today else 0
        result.append({**invoice, "client": clients.get(invoice.get("client_id"), "?"), "overdue_days": overdue})
    return sorted(result, key=lambda i: -i["overdue_days"])


def vat_period(today: date, interval: str) -> tuple[date, date]:
    start = quarter_start(today) if interval == "quarterly" else month_start(today)
    months = 3 if interval == "quarterly" else 1
    return start, add_months(start, months) - timedelta(days=1)


def output_vat(data: dict, start: date, end: date, method: str) -> float:
    """VAT owed for a period.

    ist (cash basis):    VAT share of payments received in the period (per client ratio)
    soll (accrual):      VAT of invoices dated in the period
    """
    if method == "soll":
        return round(sum(i["taxes"] for i in counted(data) if start <= parse_day(i["date"]) <= end), 2)

    share: dict = defaultdict(lambda: [0.0, 0.0])
    for invoice in counted(data):
        share[invoice["client_id"]][0] += invoice["taxes"]
        share[invoice["client_id"]][1] += invoice["amount"]

    total = 0.0
    for payment in data.get("payments", []):
        day = parse_day(payment.get("date"))
        if not day or not start <= day <= end:
            continue
        taxes, amount = share.get(payment.get("client_id"), (0.0, 0.0))
        total += payment["amount"] * (taxes / amount if amount else 0)
    return round(total, 2)


def input_vat(data: dict, start: date, end: date) -> float:
    return round(sum(e.get("tax", 0) for e in data.get("expenses", [])
                     if (d := parse_day(e.get("date"))) and start <= d <= end), 2)


def vat_liability(data: dict, today: date, interval: str, method: str) -> dict:
    start, end = vat_period(today, interval)
    out = output_vat(data, start, end, method)
    inp = input_vat(data, start, end)
    return {"start": start.isoformat(), "end": end.isoformat(), "output": out, "input": inp,
            "liability": round(out - inp, 2)}


def shares(data: dict, today: date) -> list[dict]:
    """Revenue share per client over the last 12 months."""
    start = today - timedelta(days=365)
    totals: dict = defaultdict(float)
    for invoice in counted(data):
        if parse_day(invoice["date"]) >= start:
            totals[invoice["client_id"]] += invoice["net"]
    whole = sum(totals.values())
    clients = {c["id"]: c["name"] for c in data.get("clients", [])}
    return sorted(
        [{"client_id": cid, "client": clients.get(cid, "?"), "net": round(v, 2),
          "share": v / whole if whole else 0} for cid, v in totals.items()],
        key=lambda x: -x["share"],
    )


def payment_days(data: dict) -> dict:
    """Average days from invoice to the next payment of the same client (approximation)."""
    by_client: dict = defaultdict(list)
    for payment in data.get("payments", []):
        by_client[payment.get("client_id")].append(parse_day(payment.get("date")))
    result = {}
    for client_id, paid in by_client.items():
        paid = sorted(d for d in paid if d)
        gaps = []
        for invoice in counted(data):
            if invoice["client_id"] != client_id or invoice["status"] != "paid":
                continue
            day = parse_day(invoice["date"])
            later = [d for d in paid if d >= day]
            if later:
                gaps.append((later[0] - day).days)
        if gaps:
            result[client_id] = round(sum(gaps) / len(gaps))
    return result


def summary(data: dict, today: date, interval: str = "monthly", method: str = "ist") -> dict:
    year_start = date(today.year, 1, 1)
    last_year = date(today.year - 1, today.month, today.day) if not (today.month == 2 and today.day == 29) \
        else date(today.year - 1, 2, 28)
    open_items = open_invoices(data, today)
    return {
        "revenue_ytd": revenue(data, year_start, today),
        "revenue_prev_ytd": revenue(data, date(today.year - 1, 1, 1), last_year),
        "revenue_month": revenue(data, month_start(today), today),
        "open_amount": round(sum(i["balance"] for i in open_items), 2),
        "overdue": [i for i in open_items if i["overdue_days"] > 0],
        "open": open_items,
        "vat": vat_liability(data, today, interval, method),
        "currency": data.get("currency", "EUR"),
    }


def forecast_year(data: dict, today: date) -> float:
    """Linear projection of net revenue to Dec 31 from the pace so far."""
    ytd = revenue(data, date(today.year, 1, 1), today)
    elapsed = (today - date(today.year, 1, 1)).days + 1
    days = (date(today.year, 12, 31) - date(today.year, 1, 1)).days + 1
    return round(ytd / elapsed * days, 2)


def cash_expected(data: dict, today: date, days: int) -> float:
    """Receivables plus recurring invoices due within `days` (gross)."""
    horizon = today + timedelta(days=days)
    open_amount = sum(i["balance"] for i in open_invoices(data, today))
    recurring = sum(r["amount"] for r in data.get("recurring", [])
                    if r.get("active") and (d := parse_day(r.get("next_send_date"))) and today <= d <= horizon)
    return round(open_amount + recurring, 2)
