"""Generated demo datasets, relative to today, deterministic.

The four datasets fit together so every rule fires once:
    Muster GmbH   visited on DAY_NO_BOOKING without a Kimai entry, unbilled hours, overdue invoice
    Beispiel AG   budget at 85 %, quote without reaction
    Nordlicht     EU client, invoice at 0 % without VAT id
"""

import random
from datetime import UTC, date, datetime, time, timedelta

SEED = 7
RATE = 95.0
VAT = 0.19
HOME = (52.5200, 13.4050)
CLIENT_SITE = (52.3906, 13.0645)
DAY_NO_BOOKING = 3
CLIENT_DAYS = (1, 3, 8, 10, 15)
UNBILLED_DAYS = 40
CUSTOMERS = [(1, "Muster GmbH"), (2, "Beispiel AG"), (3, "Nordlicht e.V.")]


def _weekdays_back(today: date, days: int) -> list[date]:
    found = []
    for back in range(1, days + 1):
        day = today - timedelta(days=back)
        if day.weekday() < 5:
            found.append(day)
    return found


def _stamp(day: date, hour: int, minute: int = 0) -> str:
    return datetime.combine(day, time(hour, minute), tzinfo=UTC).isoformat()


def _client_days(today: date) -> list[date]:
    return [d for d in _weekdays_back(today, 25)][: max(CLIENT_DAYS) + 1]


def kimai(today: date) -> dict:
    rnd = random.Random(SEED)
    sheets = []
    visit_days = [_client_days(today)[i] for i in CLIENT_DAYS if i < len(_client_days(today))]
    skip = _client_days(today)[DAY_NO_BOOKING]

    for index, day in enumerate(reversed(_weekdays_back(today, 600))):
        customer = 1 if day in visit_days else rnd.choice([1, 2, 2, 3])
        if day == skip:
            customer = 2
        hours = rnd.choice([5, 6, 7, 8])
        age = (today - day).days
        sheets.append({
            "id": index + 1,
            "begin": _stamp(day, 9),
            "end": _stamp(day, 9 + hours),
            "minutes": hours * 60,
            "rate": hours * RATE,
            "billable": True,
            "exported": not (customer == 1 and age <= UNBILLED_DAYS) and age > 5,
            "project_id": customer,
            "customer_id": customer,
            "activity": "vor Ort" if day in visit_days else "Entwicklung",
            "user_id": 1,
        })

    running = datetime.now(UTC) - timedelta(hours=11)
    return {
        "url": "https://kimai.demo",
        "timesheets": sheets,
        "active": [{"id": 9999, "begin": running.isoformat(), "end": None, "minutes": 0, "rate": 0,
                    "billable": True, "exported": False, "project_id": 2, "customer_id": 2,
                    "activity": "Entwicklung", "user_id": 1}],
        "projects": [
            {"id": 1, "name": "Wartung", "customer_id": 1, "budget": 0, "time_budget_min": 0,
             "budget_type": None, "end": None, "used_money": 0, "used_minutes": 0},
            {"id": 2, "name": "Relaunch", "customer_id": 2, "budget": 40000, "time_budget_min": 0,
             "budget_type": None, "end": (today + timedelta(days=60)).isoformat(),
             "used_money": 34000, "used_minutes": 0},
            {"id": 3, "name": "Mitgliederportal", "customer_id": 3, "budget": 0,
             "time_budget_min": 40 * 60, "budget_type": "month", "end": None,
             "used_money": 0, "used_minutes": 0},
        ],
        "customers": [{"id": cid, "name": name} for cid, name in CUSTOMERS],
        "absences": [{"start": (today + timedelta(days=20)).isoformat(),
                      "end": (today + timedelta(days=24)).isoformat(),
                      "type": "holiday", "status": "approved", "half_day": False}],
        "holidays": [{"date": f"{today.year}-12-25", "name": "1. Weihnachtstag", "half_day": False}],
        "holiday_bundle": True,
    }


def _month_start(day: date, back: int) -> date:
    year, month = day.year, day.month - back
    while month <= 0:
        month += 12
        year -= 1
    return date(year, month, 1)


def invoiceninja(today: date) -> dict:
    rnd = random.Random(SEED)
    invoices, payments = [], []
    number = 1
    for back in range(20, 0, -1):
        start = _month_start(today, back)
        for client_id, _name in CUSTOMERS:
            net = round(rnd.uniform(2500, 6500), 2)
            tax = 0.0 if client_id == 3 and back == 1 else round(net * VAT, 2)
            invoice_day = start + timedelta(days=2)
            invoices.append({
                "id": f"inv{number}", "number": f"R-{invoice_day.year}-{number:03d}",
                "client_id": f"c{client_id}", "status": "paid", "date": invoice_day.isoformat(),
                "due_date": (invoice_day + timedelta(days=14)).isoformat(),
                "amount": net + tax, "balance": 0.0, "taxes": tax, "net": net,
            })
            payments.append({"id": f"pay{number}", "date": (invoice_day + timedelta(days=10)).isoformat(),
                             "amount": net + tax, "client_id": f"c{client_id}"})
            number += 1

    overdue = invoices[-3]
    overdue.update(status="sent", balance=overdue["amount"],
                   due_date=(today - timedelta(days=21)).isoformat())
    payments = [p for p in payments if p["id"] != overdue["id"].replace("inv", "pay")]
    draft_day = (today - timedelta(days=10)).isoformat()
    invoices.append({"id": "inv-draft", "number": "", "client_id": "c2", "status": "draft",
                     "date": draft_day, "due_date": None, "amount": 1190.0, "balance": 1190.0,
                     "taxes": 190.0, "net": 1000.0})

    expenses = [
        {"id": "e1", "date": (today - timedelta(days=30)).isoformat(), "amount": 1428.0, "tax": 228.0,
         "notes": "Notebook ThinkPad", "vendor_id": "v1"},
        {"id": "e2", "date": (today - timedelta(days=12)).isoformat(), "amount": 59.5, "tax": 0.0,
         "notes": "Hosting", "vendor_id": "v2"},
        {"id": "e3", "date": (today - timedelta(days=5)).isoformat(), "amount": 238.0, "tax": 38.0,
         "notes": "Software", "vendor_id": "v3"},
    ]
    return {
        "url": "https://invoice.demo",
        "currency": "EUR",
        "invoices": invoices,
        "payments": payments,
        "clients": [
            {"id": "c1", "name": "Muster GmbH", "vat_number": "DE123456789", "country_id": "276"},
            {"id": "c2", "name": "Beispiel AG", "vat_number": "DE987654321", "country_id": "276"},
            {"id": "c3", "name": "Nordlicht e.V.", "vat_number": "", "country_id": "40"},
        ],
        "expenses": expenses,
        "quotes": [{"id": "q1", "number": "A-2026-004", "client_id": "c2", "status": "sent",
                    "date": (today - timedelta(days=20)).isoformat(), "amount": 8330.0}],
        "recurring": [{"id": "r1", "number": "W-01", "client_id": "c1", "active": True,
                       "next_send_date": (today + timedelta(days=12)).isoformat(),
                       "remaining_cycles": 1, "amount": 595.0}],
        "home_country_id": "276",
    }


def snipeit(today: date) -> dict:
    def ago(days: int) -> str:
        return (today - timedelta(days=days)).isoformat()

    def ahead(days: int) -> str:
        return (today + timedelta(days=days)).isoformat()

    return {
        "url": "https://assets.demo",
        "assets": [
            {"id": 1, "name": "ThinkPad T14", "tag": "NB-001", "model": "T14 Gen 4", "category": "Notebook",
             "status": "Ausgegeben", "deployable": True, "assigned": True, "purchase_date": ago(30),
             "purchase_cost": 1200.0, "warranty_expires": ahead(10), "eol_date": ahead(900),
             "next_audit": ahead(100), "last_change": ago(30)},
            {"id": 2, "name": "NAS", "tag": "SRV-002", "model": "DS920+", "category": "Server",
             "status": "In Betrieb", "deployable": True, "assigned": True, "purchase_date": ago(1900),
             "purchase_cost": 650.0, "warranty_expires": ago(800), "eol_date": ago(20),
             "next_audit": ago(15), "last_change": ago(400)},
            {"id": 3, "name": "Pixel 7", "tag": "PH-003", "model": "Pixel 7", "category": "Smartphone",
             "status": "Bereit", "deployable": True, "assigned": False, "purchase_date": ago(500),
             "purchase_cost": 599.0, "warranty_expires": ahead(230), "eol_date": None,
             "next_audit": ahead(60), "last_change": ago(140)},
            {"id": 4, "name": "Monitor 27\"", "tag": "MO-004", "model": "U2723QE", "category": "Monitor",
             "status": "Ausgegeben", "deployable": True, "assigned": True, "purchase_date": ago(60),
             "purchase_cost": 890.0, "warranty_expires": ahead(1000), "eol_date": None,
             "next_audit": ahead(200), "last_change": ago(60)},
        ],
        "licenses": [
            {"id": 1, "name": "JetBrains All Products", "expires": ahead(20), "seats": 1, "free": 0},
            {"id": 2, "name": "Microsoft 365", "expires": ahead(200), "seats": 5, "free": 2},
        ],
        "consumables": [{"id": 1, "name": "Toner schwarz", "remaining": 1, "min": 2}],
        "audit_overdue": [2],
    }


def dawarich(today: date) -> dict:
    days = _client_days(today)
    visits = []
    for index in CLIENT_DAYS:
        if index >= len(days):
            continue
        day = days[index]
        visits.append({"id": index, "start": _stamp(day, 8, 30), "end": _stamp(day, 17, 45),
                       "minutes": 555, "area_id": 2, "name": "Muster GmbH Büro",
                       "lat": CLIENT_SITE[0], "lon": CLIENT_SITE[1]})

    last = datetime.now(UTC) - timedelta(hours=2)
    return {
        "url": "https://dawarich.demo",
        "areas": [
            {"id": 1, "name": "Home", "lat": HOME[0], "lon": HOME[1], "radius": 100},
            {"id": 2, "name": "Muster GmbH Büro", "lat": CLIENT_SITE[0], "lon": CLIENT_SITE[1], "radius": 150},
        ],
        "visits": visits,
        "stats": {"totalDistanceKm": 18450},
        "last_point": last.isoformat(),
    }
