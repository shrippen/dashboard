"""Rules and metrics on the generated demo datasets (every rule fires once)."""

from datetime import date

import pytest

from app import rules  # noqa: F401  (registers rules)
from app.demo import data as demo
from app.metrics import deadlines, ninja, travel
from app.rules.base import CROSS, DEADLINES, Env, all_rules, config, for_scope

TODAY = date(2026, 9, 25)
MAPPING = {"Muster GmbH Büro": {"customer_id": 1}, "Home": {"home": True}}
SETTINGS = {"goals": {"revenue_year": 200000},
            "tax": {"vat": {"method": "ist", "return_interval": "monthly", "extension": True},
                    "prepayments": {"amount": 1200}}}


@pytest.fixture(scope="module")
def datasets():
    return {"kimai": demo.kimai(TODAY), "invoiceninja": demo.invoiceninja(TODAY),
            "snipeit": demo.snipeit(TODAY), "dawarich": demo.dawarich(TODAY)}


def _fired(scope: str, dataset: dict, datasets: dict, settings=None, today=TODAY) -> dict:
    env = Env(today, settings or SETTINGS, datasets, {"dawarich": {"areas": MAPPING}})
    found = {}
    for spec in for_scope(scope):
        for finding in spec.run(dataset, config(spec, env.settings), env):
            found.setdefault(spec.id, []).append(finding)
    return found


def test_every_rule_has_defaults():
    assert len(all_rules()) >= 30
    assert all("enabled" in r.defaults for r in all_rules())


def test_kimai_rules(datasets):
    fired = _fired("kimai", datasets["kimai"], datasets)
    assert "kimai.timer_running_long" in fired
    assert "kimai.unbilled_hours" in fired
    assert fired["kimai.unbilled_hours"][0].params["customer"] == "Muster GmbH"
    assert "kimai.budget_burn" in fired


def test_ninja_rules(datasets):
    fired = _fired("invoiceninja", datasets["invoiceninja"], datasets)
    assert fired["in.invoice_overdue"][0].message == "in.overdue_dunning"
    assert fired["in.missing_vat"][0].message == "in.missing_vat_eu"
    assert "in.expense_no_input_vat" in fired
    assert "in.quote_open" in fired and "in.draft_stale" in fired


def test_snipe_rules(datasets):
    fired = _fired("snipeit", datasets["snipeit"], datasets)
    for rule_id in ("snipe.warranty_expiring", "snipe.eol_reached", "snipe.license_expiring",
                    "snipe.license_seats", "snipe.audit_overdue", "snipe.consumable_low",
                    "snipe.unassigned_deployable", "snipe.gwg_hint"):
        assert rule_id in fired, rule_id


def test_cross_rules(datasets):
    fired = _fired(CROSS, {}, datasets)
    visits = fired["geo.visit_without_time"]
    assert len(visits) == 1 and visits[0].params["customer"] == "Muster GmbH"
    assert "snipe.expense_missing" in fired  # the monitor has no expense
    assert all(f.params["asset"] != "ThinkPad T14" for f in fired["snipe.expense_missing"])


def test_monthly_reports_only_first_week(datasets):
    early = date(2026, 10, 2)
    fired = _fired(CROSS, {}, {**datasets, "dawarich": demo.dawarich(early)}, today=early)
    assert "geo.travel_costs" in fired and "geo.per_diem" in fired
    assert "geo.travel_costs" not in _fired(CROSS, {}, datasets)


def test_disabled_rule_config():
    spec = next(r for r in all_rules() if r.id == "kimai.missing_day")
    cfg = config(spec, {"rules": {"kimai.missing_day": {"enabled": False, "bogus": 1}}})
    assert cfg["enabled"] is False and "bogus" not in cfg


def test_vat_deadlines_with_extension():
    items = deadlines.upcoming(SETTINGS, TODAY, 60)
    vat = [i for i in items if i["kind"] == "vat_return"]
    # August with Dauerfristverlängerung is due 10 October.
    assert vat[0]["due"] == date(2026, 10, 10) and vat[0]["period"] == "08/2026"
    later = deadlines.upcoming(SETTINGS, TODAY, 90)
    assert any(i["kind"] == "prepayment" and i["due"] == date(2026, 12, 10) for i in later)
    fired = _fired(DEADLINES, {}, {}, SETTINGS, today=date(2026, 9, 28))
    assert fired["tax.deadlines"][0].due == "2026-10-10"


def test_vat_methods(datasets):
    data = datasets["invoiceninja"]
    start, end = date(2026, 8, 1), date(2026, 8, 31)
    soll = ninja.output_vat(data, start, end, "soll")
    ist = ninja.output_vat(data, start, end, "ist")
    assert soll > 0 and ist > 0 and soll != ist
    assert ninja.revenue(data, start, end) > 0


def test_travel_distance(datasets):
    trips = travel.trips(datasets["dawarich"], MAPPING, date(2026, 9, 1), TODAY)
    assert trips and 60 < trips[0]["km"] < 90
