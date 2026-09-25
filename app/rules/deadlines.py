"""Deadline rules: tax dates from the space settings, with the estimated VAT liability."""

from app.enums import ServiceType, Severity
from app.metrics import deadlines as dl
from app.metrics import ninja as nm
from app.rules.base import DEADLINES, Env, Finding, day, money, rule

NINJA = ServiceType.INVOICENINJA.value


@rule("tax.deadlines", DEADLINES, notice_days=14, warn_days=3)
def deadlines(data: dict, cfg: dict, env: Env) -> list[Finding]:
    if not env.settings.get("tax"):
        return []

    found = []
    for item in dl.upcoming(env.settings, env.today, cfg["notice_days"]):
        left = (item["due"] - env.today).days
        level = Severity.WARN if left <= cfg["warn_days"] else Severity.INFO
        found.append(_finding(item, level, env))
    return found


def _finding(item: dict, level: Severity, env: Env) -> Finding:
    kind, due = item["kind"], item["due"]
    params = {"day": day(due)}
    message = f"tax.{kind}"
    ninja = env.datasets.get(NINJA)

    if kind == "vat_return":
        params["period"] = item["period"]
        if ninja:
            method = ((env.settings.get("tax") or {}).get("vat") or {}).get("method", "ist")
            out = nm.output_vat(ninja, item["period_start"], item["period_end"], method)
            inp = nm.input_vat(ninja, item["period_start"], item["period_end"])
            params["amount"] = money(out - inp)
            message = "tax.vat_return_amount"
    elif kind == "prepayment" and item.get("amount"):
        params["amount"] = money(float(item["amount"]))
        message = "tax.prepayment_amount"
    elif kind == "annual":
        params["year"] = item["year"]

    return Finding(f"{kind}:{due.isoformat()}", "tax.deadlines", level, message, params,
                   due=due.isoformat(), sources=["tax"])
