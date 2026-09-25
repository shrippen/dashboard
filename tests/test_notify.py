"""Push notifications, quiet hours, digest mail, iCal feed, trend points."""

from datetime import UTC, date, datetime

from fastapi.testclient import TestClient

from app.enums import CredentialMode, ServiceType, Severity, TokenScope
from app.outbound import mail as outbound_mail
from app.outbound import notify as outbound
from app.services import analysis, auth, connections, notify, spaces
from app.services.access import personal
from app.services.connections import Tls
from tests.conftest import make_user, who

TAX = {"tax": {"vat": {"method": "ist", "return_interval": "monthly", "extension": False}}}


def _demo_space(uid):
    space = personal(who(uid)).id
    connections.create(who(uid), space, ServiceType.INVOICENINJA, "IN", "demo://invoiceninja",
                       CredentialMode.SHARED, "", Tls.VERIFY)
    spaces.update_settings(who(uid), space, TAX)
    analysis.run_all()
    return space


def test_push_once_per_hint(app):
    uid = make_user("a@x.de")
    _demo_space(uid)
    notify.add_channel(who(uid), "ntfy", "ntfy://ntfy.lan/topic", Severity.WARN)
    outbound.SENT.clear()

    assert notify.dispatch() == 1
    assert "neue Hinweise" in outbound.SENT[0]["title"]
    assert notify.dispatch() == 0


def test_quiet_hours():
    raw = {"quiet": {"from": "22:00", "to": "07:00"}}
    assert notify.quiet_now(raw, datetime(2026, 9, 25, 21, 30, tzinfo=UTC))  # 23:30 Berlin
    assert not notify.quiet_now(raw, datetime(2026, 9, 25, 10, 0, tzinfo=UTC))


def test_digest_mail(app):
    uid = make_user("a@x.de")
    _demo_space(uid)
    notify.save_prefs(who(uid), notify.Prefs("", "", "07:00", ""))
    outbound_mail.OUTBOX.clear()
    morning = datetime(2026, 9, 25, 6, 0, tzinfo=UTC)
    assert notify.digests(morning) == 1
    assert notify.digests(morning) == 0
    assert outbound_mail.OUTBOX[-1].to == "a@x.de"


def test_calendar_feed(app):
    uid = make_user("a@x.de")
    _demo_space(uid)
    token = auth.create_token(who(uid), "cal", TokenScope.READ, [], None)
    response = TestClient(app).get(f"/calendar.ics?token={token.secret}")
    assert response.status_code == 200
    assert "BEGIN:VEVENT" in response.text and "USt-Voranmeldung" in response.text


def test_trend_points_recorded(app):
    from app.services import data

    uid = make_user("a@x.de")
    _demo_space(uid)
    conn = connections.all_raw()[0]
    assert data.points(conn, uid, "open_amount", 30)
    assert date.today().isoformat() == data.points(conn, uid, "open_amount", 30)[-1][0]


def test_notify_page(app, browser):
    make_user("a@x.de")
    assert browser("a@x.de").get("/me/notify").status_code == 200
