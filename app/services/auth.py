"""Login, sessions, second factor, API tokens, first-start setup.

    browser ──cookie(token)──► session row (hashed token)
                                  │ pending_2fa? ──► /login/totp only
                                  ▼
                               Principal (user, teams, grants, spaces)
"""

import logging
import secrets
import threading
import time
from dataclasses import dataclass
from datetime import timedelta
from enum import StrEnum

import pyotp

from app.db.base import session_scope, utcnow
from app.db.models import ApiToken, LoginSession, User
from app.enums import AuthMethod, InstanceRole, Locale, TokenScope
from app.repos import auth as repo
from app.repos import misc, users
from app.services import access, accounts, audit, crypto, mail
from app.services.access import AccessDenied, Principal
from app.services.crypto import Purpose
from app.settings import get_settings

log = logging.getLogger(__name__)

SETUP_KEY = "setup"
SETUP_CODE_LEN = 10
TOTP_ISSUER = "dashboard"
TOTP_WINDOW = 1
RECOVERY_CODES = 10
TOKEN_PREFIX_LEN = 8
TOUCH_INTERVAL = timedelta(minutes=5)

ACCOUNT_LIMIT = 5
IP_LIMIT = 20
THROTTLE_WINDOW_S = 15 * 60


class AuthError(Exception):
    """Generic login failure; the message never tells which part was wrong."""


class Throttled(AuthError):
    pass


class Step(StrEnum):
    DONE = "done"
    TOTP = "totp"


@dataclass
class LoginResult:
    token: str
    step: Step


# ── Throttling (in memory: one container, few users) ──

_fails: dict[str, list[float]] = {}
_fails_lock = threading.Lock()


def _recent(key: str, now: float) -> list[float]:
    return [t for t in _fails.get(key, []) if now - t < THROTTLE_WINDOW_S]


def _check_throttle(email: str, ip: str) -> None:
    now = time.monotonic()
    with _fails_lock:
        if len(_recent(f"a:{email.lower()}", now)) >= ACCOUNT_LIMIT:
            raise Throttled()
        if len(_recent(f"i:{ip}", now)) >= IP_LIMIT:
            raise Throttled()


def _note_fail(email: str, ip: str) -> None:
    now = time.monotonic()
    with _fails_lock:
        for key in (f"a:{email.lower()}", f"i:{ip}"):
            _fails[key] = [*_recent(key, now), now]


def _clear_fails(email: str) -> None:
    with _fails_lock:
        _fails.pop(f"a:{email.lower()}", None)


def reset_throttle() -> None:
    with _fails_lock:
        _fails.clear()


# ── First start ──


def setup_needed() -> bool:
    with session_scope() as s:
        return users.count(s) == 0


def ensure_setup_code() -> str | None:
    """Print a one-time code to the log; only it can create the first admin."""
    with session_scope() as s:
        if users.count(s) > 0:
            return None

        code = secrets.token_hex(SETUP_CODE_LEN // 2).upper()
        misc.set_setting(s, SETUP_KEY, {"hash": crypto.token_hash(code)})

    log.warning("SETUP CODE (first admin): %s", code)
    return code


def create_admin(code: str, email: str, name: str, password: str, locale: Locale) -> None:
    with session_scope() as s:
        stored = misc.setting(s, SETUP_KEY).get("hash", "")
        if users.count(s) > 0 or not stored:
            raise AuthError("setup.closed")
        if not crypto.same(stored, crypto.token_hash(code.strip().upper())):
            raise AuthError("setup.bad_code")

        user = accounts.create(s, email, name, password, InstanceRole.ADMIN, locale)
        user.is_breakglass = True
        misc.set_setting(s, SETUP_KEY, {})
        audit.log(s, user.id, "setup.admin_created")


# ── Password login ──


def login(email: str, password: str, ip: str, agent: str) -> LoginResult:
    _check_throttle(email, ip)
    with session_scope() as s:
        user = users.by_email(s, email.strip())
        stored = user.password_hash if user and user.is_active else None
        if not crypto.check_password(stored, password):
            _note_fail(email, ip)
            audit.log(s, user.id if user else None, "login.failed", target=email, ip=ip)
            raise AuthError("login.failed")

        if _oidc_only(s) and not user.is_breakglass:
            raise AuthError("login.oidc_only")

        _clear_fails(email)
        step = Step.TOTP if user.totp_enabled else Step.DONE
        token = _open_session(s, user, AuthMethod.PASSWORD, ip, agent, step)
        audit.log(s, user.id, "login.password", ip=ip)

    if not user.totp_enabled:
        mail.new_login(user.id, ip, agent)

    return LoginResult(token, step)


def _oidc_only(s) -> bool:
    return bool(misc.setting(s, "oidc").get("only"))


def _open_session(
    s, user: User, method: AuthMethod, ip: str, agent: str, step: Step, id_token: str = ""
) -> str:
    settings = get_settings()
    hours = settings.oidc_session_hours if method == AuthMethod.OIDC else settings.session_absolute_hours
    token = crypto.new_token()
    repo.add(
        s,
        LoginSession(
            token_hash=crypto.token_hash(token),
            user_id=user.id,
            method=method,
            csrf=secrets.token_hex(16),
            expires_at=utcnow() + timedelta(hours=hours),
            ip=ip[:64],
            user_agent=agent[:300],
            pending_2fa=step == Step.TOTP,
            id_token=id_token or None,
        ),
    )
    user.last_login_at = utcnow()
    return token


def open_oidc_session(user_id: int, ip: str, agent: str, id_token: str) -> str:
    with session_scope() as s:
        user = users.get(s, user_id)
        token = _open_session(s, user, AuthMethod.OIDC, ip, agent, Step.DONE, id_token)
        audit.log(s, user.id, "login.oidc", ip=ip)
    return token


# ── Session lookup ──


@dataclass
class SessionInfo:
    principal: Principal | None
    csrf: str
    pending_2fa: bool
    method: AuthMethod


def resolve(token: str | None) -> SessionInfo | None:
    if not token:
        return None

    settings = get_settings()
    now = utcnow()
    with session_scope() as s:
        row = repo.session_by_hash(s, crypto.token_hash(token))
        if row is None:
            return None

        idle_limit = row.last_seen + timedelta(minutes=settings.session_idle_minutes)
        if _aware(row.expires_at) < now or _aware(idle_limit) < now:
            repo.remove(s, row)
            return None

        if now - _aware(row.last_seen) > TOUCH_INTERVAL:
            row.last_seen = now

        who = None if row.pending_2fa else access.principal(s, row.user_id)
        if who is not None:
            who.session_id = row.id
        if who is None and not row.pending_2fa:
            repo.remove(s, row)
            return None

        return SessionInfo(who, row.csrf, row.pending_2fa, AuthMethod(row.method))


def _aware(value):
    return value if value.tzinfo else value.replace(tzinfo=utcnow().tzinfo)


def logout(token: str | None) -> str | None:
    """End the session; returns the OIDC id_token for RP-initiated logout."""
    if not token:
        return None

    with session_scope() as s:
        row = repo.session_by_hash(s, crypto.token_hash(token))
        if row is None:
            return None

        id_token = row.id_token
        audit.log(s, row.user_id, "logout")
        repo.remove(s, row)
        return id_token


def my_sessions(who: Principal) -> list[LoginSession]:
    with session_scope() as s:
        return repo.sessions_of(s, who.user_id)


def end_session(who: Principal, session_id: int) -> None:
    with session_scope() as s:
        for row in repo.sessions_of(s, who.user_id):
            if row.id == session_id:
                repo.remove(s, row)


def end_other_sessions(who: Principal) -> None:
    with session_scope() as s:
        repo.drop_sessions(s, who.user_id, keep_id=who.session_id)


def purge() -> None:
    with session_scope() as s:
        repo.purge_expired(s, utcnow())


# ── Second factor (TOTP) ──


def totp_begin(who: Principal) -> tuple[str, str]:
    """New secret (not yet active) and its otpauth URI for the QR code."""
    secret = pyotp.random_base32()
    with session_scope() as s:
        user = users.get(s, who.user_id)
        user.totp_secret_enc = crypto.encrypt(secret, Purpose.TOTP)
        user.totp_enabled = False
        uri = pyotp.TOTP(secret).provisioning_uri(user.email, issuer_name=TOTP_ISSUER)
    return secret, uri


def totp_confirm(who: Principal, code: str, ip: str) -> list[str]:
    """Activate TOTP after one valid code. Returns fresh recovery codes (shown once)."""
    with session_scope() as s:
        user = users.get(s, who.user_id)
        if not user.totp_secret_enc or not _totp_ok(user, code):
            raise AuthError("totp.invalid")

        codes = [secrets.token_hex(5) for _ in range(RECOVERY_CODES)]
        user.recovery_codes = [crypto.token_hash(c) for c in codes]
        user.totp_enabled = True
        audit.log(s, user.id, "totp.enabled", ip=ip)

    mail.security_notice(who.user_id, "totp_enabled")
    return codes


def totp_disable(who: Principal, code: str, ip: str) -> None:
    with session_scope() as s:
        user = users.get(s, who.user_id)
        if user.totp_enabled and not (_totp_ok(user, code) or _use_recovery(user, code)):
            raise AuthError("totp.invalid")

        user.totp_enabled = False
        user.totp_secret_enc = None
        user.recovery_codes = []
        audit.log(s, user.id, "totp.disabled", ip=ip)

    mail.security_notice(who.user_id, "totp_disabled")


def totp_verify(token: str, code: str, ip: str, agent: str) -> None:
    """Second login step: lift pending_2fa on success."""
    with session_scope() as s:
        row = repo.session_by_hash(s, crypto.token_hash(token))
        if row is None or not row.pending_2fa:
            raise AuthError("totp.invalid")

        user = users.get(s, row.user_id)
        _check_throttle(user.email, ip)
        if not (_totp_ok(user, code) or _use_recovery(user, code)):
            _note_fail(user.email, ip)
            raise AuthError("totp.invalid")

        row.pending_2fa = False
        user_id = user.id

    mail.new_login(user_id, ip, agent)


def _totp_ok(user: User, code: str) -> bool:
    if not user.totp_secret_enc:
        return False

    secret = crypto.decrypt(user.totp_secret_enc, Purpose.TOTP)
    return pyotp.TOTP(secret).verify(code.strip().replace(" ", ""), valid_window=TOTP_WINDOW)


def _use_recovery(user: User, code: str) -> bool:
    hashed = crypto.token_hash(code.strip().lower())
    remaining = list(user.recovery_codes or [])
    if hashed not in remaining:
        return False

    remaining.remove(hashed)
    user.recovery_codes = remaining
    return True


def totp_required(who: Principal, method: AuthMethod) -> bool:
    """Admins may be forced to set up TOTP. With OIDC, authentik owns the second factor."""
    if method == AuthMethod.OIDC or not who.is_admin:
        return False

    with session_scope() as s:
        forced = misc.setting(s, "security").get("force_admin_totp")
        user = users.get(s, who.user_id)
        return bool(forced and not user.totp_enabled)


# ── API tokens ──


@dataclass
class NewToken:
    id: int
    secret: str


def create_token(
    who: Principal, name: str, scope: TokenScope, board_ids: list[int], days: int | None
) -> NewToken:
    secret = "dsh_" + crypto.new_token()
    with session_scope() as s:
        item = ApiToken(
            user_id=who.user_id,
            name=name.strip() or scope.value,
            token_hash=crypto.token_hash(secret),
            prefix=secret[: TOKEN_PREFIX_LEN + 4],
            scope=scope,
            board_ids=board_ids,
            expires_at=utcnow() + timedelta(days=days) if days else None,
        )
        repo.add(s, item)
        audit.log(s, who.user_id, "token.created", target=item.name)
        token_id = item.id

    mail.security_notice(who.user_id, "token_created")
    return NewToken(token_id, secret)


def my_tokens(who: Principal) -> list[ApiToken]:
    with session_scope() as s:
        return repo.tokens_of(s, who.user_id)


def revoke_token(who: Principal, token_id: int) -> None:
    with session_scope() as s:
        item = repo.token(s, token_id)
        if item is None or item.user_id != who.user_id:
            raise AccessDenied("token")
        repo.remove(s, item)
        audit.log(s, who.user_id, "token.revoked", target=item.name)


def principal_for_token(secret: str, scope: TokenScope) -> Principal | None:
    with session_scope() as s:
        item = repo.token_by_hash(s, crypto.token_hash(secret))
        if item is None:
            return None
        if item.expires_at and _aware(item.expires_at) < utcnow():
            return None
        if scope == TokenScope.READ and item.scope != TokenScope.READ:
            return None

        item.last_used_at = utcnow()
        who = access.principal(s, item.user_id)
        if who is not None:
            who.token_boards = list(item.board_ids or []) or None
        return who
