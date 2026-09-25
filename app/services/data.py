"""Cached access to sources.

    key = sha256(source, connection, credential owner, params)

A shared connection is fetched once for everybody; a connection with
personal credentials once per user. Concurrent requests for the same key
wait for one fetch instead of hammering the service.
"""

import hashlib
import json
import logging
import threading
from dataclasses import dataclass
from datetime import datetime, timedelta
from enum import StrEnum

from app.db.base import session_scope, utcnow
from app.db.models import CacheEntry, Connection
from app.enums import CredentialMode
from app.repos import content
from app.repos import data as repo
from app.services import crypto
from app.services.crypto import Purpose
from app.sources import base as sources
from app.sources.base import Ctx, SourceError

log = logging.getLogger(__name__)

CACHE_RETENTION = timedelta(days=7)


class Freshness(StrEnum):
    CACHED = "cached"
    FORCE = "force"


class MissingCredential(Exception):
    """Personal credentials required but the user has none yet."""


@dataclass
class Result:
    data: dict | None
    fetched_at: datetime | None
    ok_at: datetime | None
    error: str | None

    @property
    def ok(self) -> bool:
        return self.error is None and self.data is not None


_locks: dict[str, threading.Lock] = {}
_locks_guard = threading.Lock()


def _lock(key: str) -> threading.Lock:
    with _locks_guard:
        return _locks.setdefault(key, threading.Lock())


def _key(source: str, conn_id: int | None, owner: int | None, params: dict) -> str:
    raw = json.dumps([source, conn_id, owner, params], sort_keys=True, default=str)
    return hashlib.sha256(raw.encode()).hexdigest()


def _aware(value: datetime | None) -> datetime | None:
    if value is None or value.tzinfo:
        return value

    return value.replace(tzinfo=utcnow().tzinfo)


def credential_owner(conn: Connection | None, user_id: int | None) -> int | None:
    """Cache partition: None for shared data, the user for personal credentials."""
    if conn is None or conn.credential_mode != CredentialMode.PERSONAL:
        return None

    return user_id


def _context(conn: Connection | None, user_id: int | None, params: dict) -> Ctx:
    if conn is None:
        return Ctx(params=params)

    secret = None
    if conn.credential_mode == CredentialMode.PERSONAL:
        with session_scope() as s:
            cred = content.credential(s, conn.id, user_id or 0)
            if cred is None:
                raise MissingCredential(conn.name)
            secret = crypto.decrypt(cred.secret_enc, Purpose.CREDENTIAL)
    elif conn.secret_enc:
        secret = crypto.decrypt(conn.secret_enc, Purpose.CREDENTIAL)

    return Ctx(
        url=conn.url,
        secret=secret,
        verify_tls=conn.verify_tls,
        options=dict(conn.options or {}),
        params=params,
    )


def load_connection(conn_id: int) -> Connection | None:
    with session_scope() as s:
        return content.connection(s, conn_id)


def get(
    source_key: str,
    params: dict,
    conn: Connection | None = None,
    user_id: int | None = None,
    fresh: Freshness = Freshness.CACHED,
) -> Result:
    """Cached result if fresh, otherwise fetch now. Never raises for service errors."""
    source = sources.get(source_key)
    owner = credential_owner(conn, user_id)
    key = _key(source_key, conn.id if conn else None, owner, params)

    cached = _read(key)
    if fresh == Freshness.CACHED and cached and _fresh(cached, source.ttl):
        return cached

    with _lock(key):
        cached = _read(key)
        if fresh == Freshness.CACHED and cached and _fresh(cached, source.ttl):
            return cached

        return _fetch(source, key, conn, user_id, params, cached)


def peek(source_key: str, params: dict, conn: Connection | None, user_id: int | None) -> Result:
    """Cached result only, no network. Used for instant page renders."""
    owner = credential_owner(conn, user_id)
    key = _key(source_key, conn.id if conn else None, owner, params)
    return _read(key) or Result(None, None, None, None)


def _fresh(result: Result, ttl: timedelta) -> bool:
    return result.fetched_at is not None and utcnow() - result.fetched_at < ttl


def _read(key: str) -> Result | None:
    with session_scope() as s:
        entry = repo.cache(s, key)
        if entry is None:
            return None

        return Result(entry.data, _aware(entry.fetched_at), _aware(entry.ok_at), entry.error)


def _fetch(source, key, conn, user_id, params, previous: Result | None) -> Result:
    now = utcnow()
    try:
        data = source.fetch(_context(conn, user_id, params))
        result = Result(data, now, now, None)
    except MissingCredential:
        raise
    except SourceError as exc:
        result = _failed(previous, now, str(exc))
    except Exception as exc:  # A broken source must not take the page down.
        log.exception("source %s failed", source.key)
        result = _failed(previous, now, type(exc).__name__)

    with session_scope() as s:
        repo.put_cache(
            s,
            CacheEntry(
                key=key,
                source=source.key,
                fetched_at=result.fetched_at,
                ok_at=result.ok_at,
                data=result.data,
                error=result.error,
            ),
        )

    return result


def _failed(previous: Result | None, now: datetime, error: str) -> Result:
    """Keep the last good data so widgets can show it with its age."""
    if previous and previous.data is not None:
        return Result(previous.data, now, previous.ok_at, error)

    return Result(None, now, None, error)


def prune() -> None:
    with session_scope() as s:
        repo.prune_cache(s, utcnow() - CACHE_RETENTION)
