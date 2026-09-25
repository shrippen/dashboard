"""Fresh app and database per test; helpers to create users and log in."""

import re

import pytest
from fastapi.testclient import TestClient

from app.enums import InstanceRole, Locale
from app.outbound import mail as outbound
from app.settings import get_settings

PASSWORD = "correct-horse-battery"
CSRF_RE = re.compile(r'name="csrf" value="([0-9a-f]+)"')


@pytest.fixture
def app(tmp_path, monkeypatch):
    monkeypatch.setenv("DATA_DIR", str(tmp_path))
    monkeypatch.setenv("DASHBOARD_TESTING", "1")
    monkeypatch.setenv("MASTER_KEY", "test-master-key")
    monkeypatch.setenv("SCHEDULER_ENABLED", "0")
    get_settings.cache_clear()
    outbound.OUTBOX.clear()

    from app.main import create_app
    from app.services import auth

    auth.reset_throttle()
    yield create_app()
    get_settings.cache_clear()


def make_user(email: str, role: InstanceRole = InstanceRole.USER, teams=None) -> int:
    from app.db.base import session_scope
    from app.services import accounts

    with session_scope() as s:
        user = accounts.create(s, email, email.split("@")[0], PASSWORD, role, Locale.DE)
        accounts.join_teams(s, user.id, teams or [])
        return user.id


def who(user_id: int):
    from app.db.base import session_scope
    from app.services import access

    with session_scope() as s:
        return access.principal(s, user_id)


class Browser:
    """TestClient that logs in and sends the CSRF token like a browser."""

    def __init__(self, app, email: str):
        self.client = TestClient(app)
        response = self.client.post("/login", data={"email": email, "password": PASSWORD},
                                    follow_redirects=False)
        assert response.status_code == 303, response.text
        self.csrf = self._csrf()

    def _csrf(self) -> str:
        page = self.client.get("/me").text
        return CSRF_RE.search(page).group(1)

    def get(self, url: str, **kw):
        return self.client.get(url, **kw)

    def post(self, url: str, data=None, **kw):
        data = dict(data or {})
        data["csrf"] = self.csrf
        return self.client.post(url, data=data, follow_redirects=False, **kw)


@pytest.fixture
def browser(app):
    def login(email: str) -> Browser:
        return Browser(app, email)

    return login


class Net:
    """Mocked network: routes added by tests match before the 503 fallback."""

    def __init__(self, mock):
        self._mock = mock
        self._fallback()

    def _fallback(self) -> None:
        import httpx

        self._mock.routes.pop("fallback", None)
        self._mock.route(name="fallback").mock(return_value=httpx.Response(503))

    def get(self, url: str, **kw):
        route = self._mock.get(url, **kw)
        self._fallback()
        return route

    def post(self, url: str, **kw):
        route = self._mock.post(url, **kw)
        self._fallback()
        return route


@pytest.fixture(autouse=True)
def no_network():
    """Tests never touch the network; unmatched requests get 503."""
    import respx

    with respx.mock(assert_all_called=False) as mock:
        yield Net(mock)
