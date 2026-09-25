"""Connections to services and their credentials.

    shared credentials   one token, stored on the connection, same data for all
    personal credentials every user stores his own token, data per user
Tokens are write-only: they are encrypted and never shown again.
"""

from dataclasses import dataclass
from enum import StrEnum

from sqlalchemy.orm import Session

from app.db.base import session_scope
from app.db.models import Connection, UserCredential
from app.enums import CredentialMode, ResourceKind, Right, ServiceType, SpaceKind
from app.repos import content, misc
from app.services import access, audit, crypto, data
from app.services.access import Principal, SpaceRef
from app.services.crypto import Purpose
from app.services.data import Freshness
from app.services.util import NotFound, slug, unique

SHARED_LOCATION = "dawarich_shared"


class ConnError(ValueError):
    pass


class Tls(StrEnum):
    VERIFY = "verify"
    SKIP = "skip"


@dataclass
class ConnectionView:
    id: int
    key: str
    name: str
    service: ServiceType
    url: str
    mode: CredentialMode
    has_secret: bool
    has_mine: bool
    verify_tls: bool
    options: dict
    space: SpaceRef
    right: Right


def _right(s: Session, who: Principal, conn: Connection) -> Right:
    space = access.space_of(s, who, conn.space_id)
    return access.right(who, ResourceKind.CONNECTION, conn.id, space)


def _view(s: Session, who: Principal, conn: Connection, granted: Right) -> ConnectionView:
    mine = content.credential(s, conn.id, who.user_id) is not None
    return ConnectionView(
        id=conn.id,
        key=conn.key,
        name=conn.name,
        service=ServiceType(conn.service),
        url=conn.url,
        mode=CredentialMode(conn.credential_mode),
        has_secret=bool(conn.secret_enc),
        has_mine=mine,
        verify_tls=conn.verify_tls,
        options=dict(conn.options or {}),
        space=access.space_of(s, who, conn.space_id),
        right=granted,
    )


def listing(who: Principal, minimum: Right = Right.USE) -> list[ConnectionView]:
    with session_scope() as s:
        found = content.connections(s, list(who.spaces))
        shared = [rid for (kind, rid) in who.grants if kind == ResourceKind.CONNECTION]
        found += [c for c in (content.connection(s, rid) for rid in shared)
                  if c and c.space_id not in who.spaces]
        result = []
        for conn in found:
            granted = _right(s, who, conn)
            if granted >= minimum:
                result.append(_view(s, who, conn, granted))
        return result


def get(who: Principal, conn_id: int) -> ConnectionView:
    with session_scope() as s:
        conn = content.connection(s, conn_id)
        if conn is None:
            raise NotFound("connection")
        granted = _right(s, who, conn)
        access.need(granted, Right.USE)
        return _view(s, who, conn, granted)


def _check_location_sharing(s: Session, service: ServiceType, mode: CredentialMode,
                            space: SpaceRef) -> None:
    """Location history stays personal unless the admin allows sharing."""
    if service != ServiceType.DAWARICH or mode == CredentialMode.PERSONAL:
        return
    if space.kind == SpaceKind.PERSONAL:
        return
    if not misc.setting(s, SHARED_LOCATION).get("allowed"):
        raise ConnError("connection.location_personal")


def create(who: Principal, space_id: int, service: ServiceType, name: str, url: str,
           mode: CredentialMode, secret: str, tls: Tls = Tls.VERIFY,
           options: dict | None = None) -> int:
    with session_scope() as s:
        space = access.space_of(s, who, space_id)
        need = Right.MANAGE if space and space.kind == SpaceKind.TEAM else Right.EDIT
        access.need(access.space_right(who, space), need)
        _check_location_sharing(s, service, mode, space)

        taken = {c.key for c in content.connections(s, [space_id])}
        conn = content.add(
            s,
            Connection(
                space_id=space_id,
                key=unique(slug(name or service.value, service.value), taken),
                name=name.strip() or service.value,
                service=service.value,
                url=url.strip().rstrip("/"),
                credential_mode=mode,
                secret_enc=crypto.encrypt(secret, Purpose.CREDENTIAL) if secret else None,
                verify_tls=tls == Tls.VERIFY,
                options=options or {},
            ),
        )
        audit.log(s, who.user_id, "connection.created", target=conn.name)
        return conn.id


def update(who: Principal, conn_id: int, name: str, url: str, mode: CredentialMode,
           secret: str | None, tls: Tls, options: dict | None = None) -> None:
    with session_scope() as s:
        conn = content.connection(s, conn_id)
        if conn is None:
            raise NotFound("connection")
        access.need(_right(s, who, conn), Right.MANAGE)
        space = access.space_of(s, who, conn.space_id)
        _check_location_sharing(s, ServiceType(conn.service), mode, space)

        conn.name = name.strip() or conn.name
        conn.url = url.strip().rstrip("/")
        conn.credential_mode = mode
        conn.verify_tls = tls == Tls.VERIFY
        if options is not None:
            conn.options = options
        if secret:
            conn.secret_enc = crypto.encrypt(secret, Purpose.CREDENTIAL)
            audit.log(s, who.user_id, "connection.secret_changed", target=conn.name)
        audit.log(s, who.user_id, "connection.updated", target=conn.name)


def delete(who: Principal, conn_id: int) -> None:
    with session_scope() as s:
        conn = content.connection(s, conn_id)
        if conn is None:
            return
        access.need(_right(s, who, conn), Right.MANAGE)
        misc.drop_shares(s, ResourceKind.CONNECTION, conn.id)
        audit.log(s, who.user_id, "connection.deleted", target=conn.name)
        content.remove(s, conn)


def set_mine(who: Principal, conn_id: int, secret: str) -> None:
    """Store the user's personal token for a connection he may use."""
    with session_scope() as s:
        conn = content.connection(s, conn_id)
        if conn is None:
            raise NotFound("connection")
        access.need(_right(s, who, conn), Right.VIEW)
        cred = content.credential(s, conn.id, who.user_id)
        blob = crypto.encrypt(secret, Purpose.CREDENTIAL)
        if cred is None:
            content.add(s, UserCredential(connection_id=conn.id, user_id=who.user_id, secret_enc=blob))
        else:
            cred.secret_enc = blob
        audit.log(s, who.user_id, "credential.set", target=conn.name)


def drop_mine(who: Principal, conn_id: int) -> None:
    with session_scope() as s:
        cred = content.credential(s, conn_id, who.user_id)
        if cred:
            content.remove(s, cred)


def personal_needed(who: Principal) -> list[ConnectionView]:
    """Connections with personal credentials the user can see."""
    return [c for c in listing(who, Right.VIEW) if c.mode == CredentialMode.PERSONAL]


@dataclass
class TestResult:
    ok: bool
    message: str
    version: str | None = None


def test(who: Principal, conn_id: int) -> TestResult:
    """Call the service's test source (version check) with the stored credentials."""
    with session_scope() as s:
        conn = content.connection(s, conn_id)
        if conn is None:
            raise NotFound("connection")
        access.need(_right(s, who, conn), Right.USE)

    try:
        result = data.get(f"{conn.service}.test", {}, conn, who.user_id, Freshness.FORCE)
    except data.MissingCredential:
        return TestResult(False, "credential.missing")
    except KeyError:
        return TestResult(False, "source.unknown")

    if not result.ok:
        return TestResult(False, result.error or "error")
    return TestResult(True, "ok", (result.data or {}).get("version"))


def by_id(conn_id: int) -> Connection | None:
    """Raw connection for internal jobs (rules). No access check: callers are jobs."""
    with session_scope() as s:
        return content.connection(s, conn_id)


def all_raw() -> list[Connection]:
    with session_scope() as s:
        return content.all_connections(s)


def credential_users(conn_id: int) -> list[int]:
    with session_scope() as s:
        return [c.user_id for c in content.credentials(s, conn_id)]
