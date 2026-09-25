"""Single sign-on with authentik (OIDC, authorization code + PKCE).

    /auth/oidc/login ──► authentik ──► /auth/oidc/callback
                                          │ state/nonce/verifier (server memory, 10 min)
                                          ▼
                     user by sub │ link to logged-in user │ verified e-mail │ auto-create
                                          │
                     groups ──► initial role and teams (only when the account is created)
"""

import base64
import hashlib
import secrets
import threading
import time
from dataclasses import dataclass, field
from urllib.parse import urlencode

from fastapi import Request

from app.db.base import session_scope
from app.enums import InstanceRole, Locale, TeamRole
from app.repos import misc, users
from app.services import accounts, audit, crypto
from app.services.access import AccessDenied, Principal
from app.services.crypto import Purpose
from app.settings import get_settings
from app.sources import oidc as provider_api
from app.sources.base import SourceError

SETTING = "oidc"
STATE_TTL_S = 600
SCOPES = "openid profile email"
CALLBACK = "/auth/oidc/callback"
GROUPS_PREF = "oidc_groups"


class OidcError(Exception):
    pass


@dataclass
class GroupRule:
    group: str
    role: InstanceRole | None = None
    team: str | None = None
    team_role: TeamRole = TeamRole.VIEWER


@dataclass
class OidcConfig:
    enabled: bool = False
    issuer: str = ""
    client_id: str = ""
    has_secret: bool = False
    label: str = "authentik"
    only: bool = False
    auto_create: bool = True
    email_link: bool = False
    rules: list[GroupRule] = field(default_factory=list)


@dataclass
class _Pending:
    nonce: str
    verifier: str
    next: str
    link_user: int | None
    at: float


_pending: dict[str, _Pending] = {}
_pending_lock = threading.Lock()


# ── Configuration ──


def _raw() -> dict:
    with session_scope() as s:
        raw = misc.setting(s, SETTING)

    settings = get_settings()
    if not raw.get("issuer") and settings.oidc_issuer:
        raw = {"enabled": True, "issuer": settings.oidc_issuer,
               "client_id": settings.oidc_client_id or "", **raw}
    return raw


def config() -> OidcConfig:
    raw = _raw()
    rules = [
        GroupRule(
            group=r["group"],
            role=InstanceRole(r["role"]) if r.get("role") else None,
            team=r.get("team") or None,
            team_role=TeamRole(r.get("team_role", TeamRole.VIEWER)),
        )
        for r in raw.get("rules", [])
    ]
    return OidcConfig(
        enabled=bool(raw.get("enabled")),
        issuer=raw.get("issuer", ""),
        client_id=raw.get("client_id", ""),
        has_secret=bool(raw.get("secret_enc") or get_settings().oidc_client_secret),
        label=raw.get("label", "authentik"),
        only=bool(raw.get("only")),
        auto_create=raw.get("auto_create", True),
        email_link=bool(raw.get("email_link")),
        rules=rules,
    )


def save(who: Principal, cfg: OidcConfig, secret: str | None) -> None:
    if not who.is_admin:
        raise AccessDenied("oidc")

    with session_scope() as s:
        raw = misc.setting(s, SETTING)
        raw.update(
            enabled=cfg.enabled,
            issuer=cfg.issuer.strip(),
            client_id=cfg.client_id.strip(),
            label=cfg.label.strip() or "authentik",
            only=cfg.only,
            auto_create=cfg.auto_create,
            email_link=cfg.email_link,
            rules=[
                {"group": r.group, "role": r.role.value if r.role else None, "team": r.team,
                 "team_role": r.team_role.value}
                for r in cfg.rules if r.group.strip()
            ],
        )
        if secret:
            raw["secret_enc"] = base64.b64encode(crypto.encrypt(secret, Purpose.SETTING)).decode()
        misc.set_setting(s, SETTING, raw)
        audit.log(s, who.user_id, "oidc.saved")


def _secret() -> str:
    raw = _raw()
    if raw.get("secret_enc"):
        return crypto.decrypt(base64.b64decode(raw["secret_enc"]), Purpose.SETTING)
    return get_settings().oidc_client_secret or ""


def button() -> str | None:
    cfg = config()
    return cfg.label if cfg.enabled and cfg.issuer and cfg.client_id else None


def test(who: Principal) -> str:
    if not who.is_admin:
        raise AccessDenied("oidc")
    try:
        return provider_api.discover(config().issuer).issuer
    except SourceError as exc:
        raise OidcError(str(exc)) from exc


# ── Flow ──


def _redirect_uri() -> str:
    return get_settings().base_url.rstrip("/") + CALLBACK


def _challenge(verifier: str) -> str:
    digest = hashlib.sha256(verifier.encode()).digest()
    return base64.urlsafe_b64encode(digest).rstrip(b"=").decode()


def _cleanup(now: float) -> None:
    for key in [k for k, v in _pending.items() if now - v.at > STATE_TTL_S]:
        _pending.pop(key, None)


def authorize_url(_request: Request, next_url: str, link_user: int | None = None) -> str:
    cfg = config()
    if not button():
        raise OidcError("oidc.disabled")
    try:
        provider = provider_api.discover(cfg.issuer)
    except SourceError as exc:
        raise OidcError("oidc.unreachable") from exc

    state, nonce, verifier = (secrets.token_urlsafe(24) for _ in range(3))
    now = time.monotonic()
    with _pending_lock:
        _cleanup(now)
        _pending[state] = _Pending(nonce, verifier, next_url, link_user, now)

    query = {
        "response_type": "code",
        "client_id": cfg.client_id,
        "redirect_uri": _redirect_uri(),
        "scope": SCOPES,
        "state": state,
        "nonce": nonce,
        "code_challenge": _challenge(verifier),
        "code_challenge_method": "S256",
    }
    return f"{provider.authorize}?{urlencode(query)}"


def complete(request: Request) -> tuple[int, str, str]:
    """Finish the login. Returns (user id, id_token, next url)."""
    params = request.query_params
    if params.get("error"):
        raise OidcError("oidc.denied")

    with _pending_lock:
        pending = _pending.pop(params.get("state", ""), None)
    if pending is None or time.monotonic() - pending.at > STATE_TTL_S:
        raise OidcError("oidc.state")

    cfg = config()
    try:
        provider = provider_api.discover(cfg.issuer)
        tokens = provider_api.exchange(provider, params.get("code", ""), _redirect_uri(),
                                       cfg.client_id, _secret(), pending.verifier)
        claims = provider_api.claims(provider, tokens.get("id_token", ""), cfg.client_id,
                                     pending.nonce)
    except SourceError as exc:
        raise OidcError("oidc.failed") from exc

    user_id = _account(cfg, claims, pending.link_user)
    return user_id, tokens.get("id_token", ""), pending.next


def _account(cfg: OidcConfig, claims: dict, link_user: int | None) -> int:
    sub = str(claims.get("sub", ""))
    email = str(claims.get("email", "")).strip()
    groups = [str(g) for g in claims.get("groups", []) or []]
    if not sub:
        raise OidcError("oidc.failed")

    with session_scope() as s:
        user = users.by_sub(s, sub)
        if link_user is not None:
            if user is not None and user.id != link_user:
                raise OidcError("oidc.sub_taken")
            user = users.get(s, link_user)
            user.oidc_sub = sub
            audit.log(s, user.id, "oidc.linked")
        elif user is None and cfg.email_link and claims.get("email_verified") and email:
            user = users.by_email(s, email)
            if user is not None:
                user.oidc_sub = sub
                audit.log(s, user.id, "oidc.linked_by_email")
        if user is None:
            if not cfg.auto_create or not email:
                raise OidcError("oidc.no_account")
            user = _create(s, cfg, claims, sub, email, groups)

        if not user.is_active:
            raise OidcError("login.failed")

        prefs = dict(user.prefs or {})
        prefs[GROUPS_PREF] = groups
        user.prefs = prefs
        return user.id


def _create(s, cfg: OidcConfig, claims: dict, sub: str, email: str, groups: list[str]):
    name = claims.get("name") or claims.get("preferred_username") or email
    role, teams = initial_values(cfg.rules, groups)
    user = accounts.create(s, email, name, None, role, Locale.DE, sub=sub)
    accounts.join_teams(s, user.id, teams)
    audit.log(s, user.id, "oidc.account_created", groups=groups)
    return user


def initial_values(rules: list[GroupRule], groups: list[str]) -> tuple[InstanceRole, list[dict]]:
    """Start role and teams from authentik groups. Later logins do not change them."""
    role = InstanceRole.USER
    teams: dict[str, TeamRole] = {}
    rank = {TeamRole.VIEWER: 1, TeamRole.EDITOR: 2, TeamRole.OWNER: 3}
    for rule in rules:
        if rule.group not in groups:
            continue
        if rule.role == InstanceRole.ADMIN:
            role = InstanceRole.ADMIN
        if rule.team:
            current = teams.get(rule.team)
            if current is None or rank[rule.team_role] > rank[current]:
                teams[rule.team] = rule.team_role
    return role, [{"team": name, "role": r.value} for name, r in teams.items()]


@dataclass
class Reapply:
    role: InstanceRole
    teams: list[dict]
    groups: list[str]


def preview_reapply(who: Principal, user_id: int) -> Reapply:
    if not who.is_admin:
        raise AccessDenied("oidc")
    with session_scope() as s:
        user = users.get(s, user_id)
        groups = list((user.prefs or {}).get(GROUPS_PREF, []))
    role, teams = initial_values(config().rules, groups)
    return Reapply(role, teams, groups)


def reapply(who: Principal, user_id: int, ip: str) -> None:
    """Admin action: set role and teams again from the last seen authentik groups."""
    plan = preview_reapply(who, user_id)
    with session_scope() as s:
        user = users.get(s, user_id)
        if user.id != who.user_id or plan.role == InstanceRole.ADMIN:
            user.role = plan.role
        accounts.join_teams(s, user.id, plan.teams)
        audit.log(s, who.user_id, "oidc.reapplied", target=str(user_id), ip=ip,
                  role=plan.role.value, teams=plan.teams)


def logout_url(id_token: str) -> str | None:
    cfg = config()
    try:
        provider = provider_api.discover(cfg.issuer)
    except SourceError:
        return None
    if not provider.end_session:
        return None
    query = {"id_token_hint": id_token,
             "post_logout_redirect_uri": get_settings().base_url.rstrip("/") + "/login"}
    return f"{provider.end_session}?{urlencode(query)}"
