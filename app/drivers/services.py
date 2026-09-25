"""Raw REST clients for the four services. Only HTTP, paging and auth live here.

    KimaiApi        Bearer token, pages via X-Total-Pages
    NinjaApi        X-API-TOKEN, pages via meta.pagination
    SnipeApi        Bearer token, pages via limit/offset
    DawarichApi     api_key query parameter
"""

from dataclasses import dataclass

from app.drivers import http

OK_MAX = 299
NOT_FOUND = 404
KIMAI_PAGE = 250
NINJA_PAGE = 100
SNIPE_PAGE = 500
MAX_PAGES = 200


class ApiError(Exception):
    """Service answered with an error or could not be reached."""


class ApiMissing(ApiError):
    """Endpoint does not exist (e.g. plugin not installed)."""


def _json(url: str, headers: dict, params: dict | None, verify: bool):
    try:
        response = http.request("GET", url, headers=headers, params=params, verify=verify)
    except (http.HttpError, http.EgressDenied) as exc:
        raise ApiError(str(exc)) from exc

    if response.status_code == NOT_FOUND:
        raise ApiMissing(url)
    if response.status_code > OK_MAX:
        raise ApiError(f"HTTP {response.status_code}")
    try:
        return response.json(), response.headers
    except ValueError as exc:
        raise ApiError("invalid JSON") from exc


@dataclass(frozen=True)
class KimaiApi:
    url: str
    token: str
    verify: bool = True

    def _headers(self) -> dict:
        return {"Authorization": f"Bearer {self.token}", "Accept": "application/json"}

    def get(self, path: str, params: dict | None = None):
        body, _headers = _json(f"{self.url}/api/{path}", self._headers(), params, self.verify)
        return body

    def pages(self, path: str, params: dict) -> list[dict]:
        items: list[dict] = []
        for page in range(1, MAX_PAGES + 1):
            query = {**params, "page": page, "size": KIMAI_PAGE}
            body, headers = _json(f"{self.url}/api/{path}", self._headers(), query, self.verify)
            items += body or []
            if page >= int(headers.get("X-Total-Pages", "1") or 1):
                break
        return items


@dataclass(frozen=True)
class NinjaApi:
    url: str
    token: str
    verify: bool = True

    def _headers(self) -> dict:
        return {"X-API-TOKEN": self.token, "X-Requested-With": "XMLHttpRequest",
                "Accept": "application/json"}

    def version(self) -> str | None:
        _body, headers = _json(f"{self.url}/api/v1/ping", self._headers(), None, self.verify)
        return headers.get("X-App-Version")

    def pages(self, entity: str, params: dict | None = None) -> list[dict]:
        items: list[dict] = []
        for page in range(1, MAX_PAGES + 1):
            query = {**(params or {}), "per_page": NINJA_PAGE, "page": page}
            body, _headers = _json(f"{self.url}/api/v1/{entity}", self._headers(), query, self.verify)
            items += body.get("data", [])
            total = body.get("meta", {}).get("pagination", {}).get("total_pages", 1)
            if page >= int(total or 1):
                break
        return items


@dataclass(frozen=True)
class SnipeApi:
    url: str
    token: str
    verify: bool = True

    def _headers(self) -> dict:
        return {"Authorization": f"Bearer {self.token}", "Accept": "application/json"}

    def get(self, path: str, params: dict | None = None):
        body, _headers = _json(f"{self.url}/api/v1/{path}", self._headers(), params, self.verify)
        return body

    def rows(self, path: str, params: dict | None = None) -> list[dict]:
        items: list[dict] = []
        offset = 0
        for _ in range(MAX_PAGES):
            query = {**(params or {}), "limit": SNIPE_PAGE, "offset": offset}
            body = self.get(path, query)
            rows = body.get("rows", []) if isinstance(body, dict) else []
            items += rows
            offset += len(rows)
            if not rows or offset >= int(body.get("total", 0) or 0):
                break
        return items


@dataclass(frozen=True)
class DawarichApi:
    url: str
    token: str
    verify: bool = True

    def get(self, path: str, params: dict | None = None):
        query = {**(params or {}), "api_key": self.token}
        body, _headers = _json(f"{self.url}/api/v1/{path}", {"Accept": "application/json"}, query,
                               self.verify)
        return body

    def version(self) -> str | None:
        _body, headers = _json(f"{self.url}/api/v1/health", {}, {"api_key": self.token}, self.verify)
        return headers.get("X-Dawarich-Version")
