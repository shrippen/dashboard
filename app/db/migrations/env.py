"""Alembic environment: uses the app's engine and metadata."""

from alembic import context

from app.db import models  # noqa: F401  (registers tables)
from app.db.base import Base, engine, init_engine
from app.settings import get_settings

config = context.config
target = Base.metadata


def _connectable():
    connection = config.attributes.get("connection")
    if connection is not None:
        return connection

    try:
        return engine()
    except RuntimeError:
        return init_engine(get_settings().db_url)


def run() -> None:
    connectable = _connectable()
    if hasattr(connectable, "connect"):
        with connectable.connect() as conn:
            _migrate(conn)
        return

    _migrate(connectable)


def _migrate(conn) -> None:
    # Batch mode: SQLite cannot ALTER most constraints in place.
    context.configure(connection=conn, target_metadata=target, render_as_batch=True)
    with context.begin_transaction():
        context.run_migrations()


run()
