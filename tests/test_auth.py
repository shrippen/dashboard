from fastapi.testclient import TestClient

from app.enums import InstanceRole
from app.outbound import mail as outbound
from tests.conftest import PASSWORD, make_user


def test_setup_code_creates_first_admin(app):
    from app.services import auth

    client = TestClient(app)
    assert client.get("/login", follow_redirects=False).headers["location"] == "/setup"

    bad = client.post("/setup", data={"code": "WRONG", "email": "a@x.de", "password": PASSWORD})
    assert bad.status_code == 400

    code = auth.ensure_setup_code()
    ok = client.post("/setup", data={"code": code, "email": "a@x.de", "password": PASSWORD},
                     follow_redirects=False)
    assert ok.status_code == 303
    assert not auth.setup_needed()
    assert auth.ensure_setup_code() is None


def test_login_wrong_password_and_throttle(app):
    make_user("u@x.de")
    client = TestClient(app)
    for _ in range(5):
        assert client.post("/login", data={"email": "u@x.de", "password": "nope"}).status_code == 401
    blocked = client.post("/login", data={"email": "u@x.de", "password": PASSWORD})
    assert blocked.status_code == 429


def test_protected_pages_redirect_to_login(app):
    make_user("u@x.de")
    client = TestClient(app)
    response = client.get("/library", follow_redirects=False)
    assert response.status_code == 303
    assert response.headers["location"].startswith("/login")


def test_post_without_csrf_is_rejected(app, browser):
    make_user("u@x.de")
    b = browser("u@x.de")
    response = b.client.post("/boards/new", data={"name": "X", "space_id": 1}, follow_redirects=False)
    assert response.status_code == 403


def test_totp_second_step(app, browser):
    import pyotp

    from app.services import auth
    from tests.conftest import who

    uid = make_user("u@x.de")
    secret, _uri = auth.totp_begin(who(uid))
    codes = auth.totp_confirm(who(uid), pyotp.TOTP(secret).now(), "")
    assert len(codes) == 10

    client = TestClient(app)
    step1 = client.post("/login", data={"email": "u@x.de", "password": PASSWORD}, follow_redirects=False)
    assert step1.headers["location"].startswith("/login/totp")
    assert client.get("/library", follow_redirects=False).headers["location"] == "/login/totp"

    client.post("/login/totp", data={"code": codes[0]}, follow_redirects=False)
    assert client.get("/library").status_code == 200


def test_api_token_scope(app):
    from app.enums import TokenScope
    from app.services import auth
    from tests.conftest import who

    uid = make_user("u@x.de")
    token = auth.create_token(who(uid), "cli", TokenScope.READ, [], 30)
    client = TestClient(app)
    assert client.get("/api/summary").status_code == 403
    ok = client.get("/api/summary", headers={"Authorization": f"Bearer {token.secret}"})
    assert ok.status_code == 200
    assert "hints" in ok.json()


def test_invite_and_reset_mail(app, browser):
    from app.enums import Locale
    from app.services import invites
    from tests.conftest import who

    admin = make_user("admin@x.de", InstanceRole.ADMIN)
    teams = [{"team": "IT", "role": "editor"}]
    link = invites.create(who(admin), "new@x.de", InstanceRole.USER, teams, Locale.EN)
    assert outbound.OUTBOX[-1].to == "new@x.de"

    token = link.rsplit("/", 1)[1]
    client = TestClient(app)
    form = {"name": "New", "password": PASSWORD}
    response = client.post(f"/invite/{token}", data=form, follow_redirects=False)
    assert response.status_code == 303
    assert invites.peek(token) is None

    invites.request_reset("new@x.de", "")
    assert "reset" in outbound.OUTBOX[-1].text
