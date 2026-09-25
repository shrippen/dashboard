"""Tax deadlines of a freelancer liable to VAT (Germany), from space settings.

    settings["tax"] = {"vat": {"return_interval": "monthly", "extension": true},
                       "prepayments": {"amount": 1200}, "annual_due": "07-31"}
"""

from datetime import date, timedelta

from app.metrics.dates import add_months

DUE_DAY = 10
PREPAYMENT_MONTHS = (3, 6, 9, 12)
DEFAULT_ANNUAL = "07-31"
QUARTER = 3


def _vat_returns(today: date, tax: dict, horizon: date) -> list[dict]:
    """Voranmeldung due on the 10th after the period (+1 month with Dauerfristverlängerung)."""
    vat = tax.get("vat") or {}
    months = QUARTER if vat.get("return_interval") == "quarterly" else 1
    shift = 1 if vat.get("extension") else 0
    found = []
    start = add_months(today, -12)
    while start <= horizon:
        if months == 1 or (start.month - 1) % QUARTER == 0:
            period_end = add_months(start, months) - timedelta(days=1)
            due = add_months(period_end, 1 + shift).replace(day=DUE_DAY)
            if today <= due <= horizon:
                label = start.strftime("%m/%Y") if months == 1 else f"Q{(start.month - 1) // 3 + 1}/{start.year}"
                found.append({"kind": "vat_return", "due": due, "period": label,
                              "period_start": start, "period_end": period_end})
        start = add_months(start, 1)
    return found


def _prepayments(today: date, tax: dict, horizon: date) -> list[dict]:
    amount = (tax.get("prepayments") or {}).get("amount")
    found = []
    for year in (today.year, today.year + 1):
        for month in PREPAYMENT_MONTHS:
            due = date(year, month, DUE_DAY)
            if today <= due <= horizon:
                found.append({"kind": "prepayment", "due": due, "amount": amount})
    return found


def _annual(today: date, tax: dict, horizon: date) -> list[dict]:
    """VAT and income tax returns for last year (default: 31 July, without tax advisor)."""
    month, day_ = (int(x) for x in str(tax.get("annual_due") or DEFAULT_ANNUAL).split("-"))
    found = []
    for year in (today.year, today.year + 1):
        due = date(year, month, day_)
        if today <= due <= horizon:
            found.append({"kind": "annual", "due": due, "year": year - 1})
    return found


def upcoming(settings: dict, today: date, days: int) -> list[dict]:
    tax = settings.get("tax") or {}
    horizon = today + timedelta(days=days)
    items = _vat_returns(today, tax, horizon) + _prepayments(today, tax, horizon) + _annual(today, tax, horizon)
    return sorted(items, key=lambda i: i["due"])
