"""Engine and session handling.

SQLite runs in WAL mode so readers (widget fragments) never block the
single writer (scheduler, editor).
"""

from collections.abc import Iterator
from contextlib import contextmanager
from datetime import UTC, datetime

from sqlalchemy import JSON, DateTime, MetaData, create_engine, event
from sqlalchemy.engine import Engine
from sqlalchemy.orm import DeclarativeBase, Session, sessionmaker

NAMING = {
    "ix": "ix_%(column_0_label)s",
    "uq": "uq_%(table_name)s_%(column_0_name)s",
    "ck": "ck_%(table_name)s_%(constraint_name)s",
    "fk": "fk_%(table_name)s_%(column_0_name)s_%(referred_table_name)s",
    "pk": "pk_%(table_name)s",
}
SQLITE_BUSY_MS = 5000


def utcnow() -> datetime:
    return datetime.now(UTC)


class Base(DeclarativeBase):
    metadata = MetaData(naming_convention=NAMING)
    type_annotation_map = {dict: JSON, list: JSON, datetime: DateTime(timezone=True)}


_engine: Engine | None = None
_factory: sessionmaker[Session] | None = None


def _sqlite_pragmas(dbapi_conn, _record) -> None:
    cursor = dbapi_conn.cursor()
    cursor.execute("PRAGMA journal_mode=WAL")
    cursor.execute("PRAGMA foreign_keys=ON")
    cursor.execute(f"PRAGMA busy_timeout={SQLITE_BUSY_MS}")
    cursor.close()


def init_engine(url: str) -> Engine:
    global _engine, _factory

    is_sqlite = url.startswith("sqlite")
    args = {"check_same_thread": False} if is_sqlite else {}
    _engine = create_engine(url, connect_args=args, future=True)
    if is_sqlite:
        event.listen(_engine, "connect", _sqlite_pragmas)

    _factory = sessionmaker(bind=_engine, expire_on_commit=False, future=True)
    return _engine


def engine() -> Engine:
    if _engine is None:
        raise RuntimeError("engine not initialised")

    return _engine


@contextmanager
def session_scope() -> Iterator[Session]:
    """One unit of work: commit on success, roll back on error."""
    if _factory is None:
        raise RuntimeError("engine not initialised")

    session = _factory()
    try:
        yield session
        session.commit()
    except Exception:
        session.rollback()
        raise
    finally:
        session.close()
