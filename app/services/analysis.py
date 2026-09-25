"""Runs all rules and reconciles hints (scheduler job, every 5 minutes).

    for each space
      for each owner (None = shared data, user id = personal credentials)
        datasets  ← <service>.data per connection (cached, 10 min)
        service rules  → hints (per connection)
        cross + deadline rules → hints (per space and owner)
"""

import logging
from dataclasses import dataclass, field
from datetime import date

from app.db.base import session_scope
from app.db.models import Connection
from app.enums import CredentialMode, Severity
from app.metrics import kimai as km
from app.metrics import ninja as nm
from app.repos import content
from app.repos import data as data_repo
from app.rules import base as rules
from app.rules.base import CROSS, DEADLINES, Env, Finding
from app.services import data, hints
from app.services.data import MissingCredential

log = logging.getLogger(__name__)

CONNECTOR_RULE = "system.connector_down"
SNAPSHOT_METRICS = ("revenue_ytd", "open_amount", "month_min")


@dataclass
class _Scope:
    """Datasets of one space for one owner."""

    space_id: int
    owner: int | None
    settings: dict
    datasets: dict[str, dict] = field(default_factory=dict)
    options: dict[str, dict] = field(default_factory=dict)


def run_all(today: date | None = None) -> int:
    """Returns the number of new or reopened hints (for notifications)."""
    today = today or date.today()
    with session_scope() as s:
        spaces = [(sp.id, dict(sp.settings or {})) for sp in content.all_spaces(s)]
        conns = content.all_connections(s)
        owners = {c.id: [cred.user_id for cred in content.credentials(s, c.id)] for c in conns}

    fresh = 0
    for space_id, settings in spaces:
        mine = [c for c in conns if c.space_id == space_id]
        scopes: dict[int | None, _Scope] = {None: _Scope(space_id, None, settings)}
        for conn in mine:
            fresh += _run_connection(conn, owners.get(conn.id, []), scopes, settings, today)
        for scope in scopes.values():
            fresh += _run_scope(scope, today)
    return fresh


def _owners(conn: Connection, users: list[int]) -> list[int | None]:
    return users if conn.credential_mode == CredentialMode.PERSONAL else [None]


def _run_connection(conn: Connection, users: list[int], scopes: dict, settings: dict, today: date) -> int:
    fresh = 0
    for owner in _owners(conn, users):
        scope = scopes.setdefault(owner, _Scope(conn.space_id, owner, settings))
        try:
            result = data.get(f"{conn.service}.data", {}, conn, owner)
        except (MissingCredential, KeyError):
            continue

        down = [] if result.ok else [_down(conn, result.error)]
        fresh += hints.sync(conn.space_id, owner, conn.id, [CONNECTOR_RULE], down)
        if result.data is None:
            continue

        scope.datasets[conn.service] = result.data
        scope.options[conn.service] = dict(conn.options or {})
        env = Env(today, settings, scope.datasets, scope.options)
        findings, ids = _apply(rules.for_scope(conn.service), result.data, env)
        fresh += hints.sync(conn.space_id, owner, conn.id, ids, findings)
        _snapshot(conn, owner, result.data, today)
    return fresh


def _run_scope(scope: _Scope, today: date) -> int:
    """Cross-service and deadline rules. Personal scopes inherit the shared datasets."""
    env = Env(today, scope.settings, scope.datasets, scope.options)
    specs = rules.for_scope(CROSS) + rules.for_scope(DEADLINES)
    findings, ids = _apply(specs, {}, env)
    return hints.sync(scope.space_id, scope.owner, None, ids, findings)


def _apply(specs: list, dataset: dict, env: Env) -> tuple[list[Finding], list[str]]:
    findings: list[Finding] = []
    ids = []
    for spec in specs:
        cfg = rules.config(spec, env.settings)
        ids.append(spec.id)
        if not cfg.get(rules.ENABLED, True):
            continue
        try:
            findings += spec.run(dataset, cfg, env)
        except Exception:  # A broken rule must not stop the others.
            log.exception("rule %s failed", spec.id)
    return findings, ids


def _down(conn: Connection, error: str | None) -> Finding:
    return Finding(f"down:{conn.id}", CONNECTOR_RULE, Severity.WARN, "system.connector_down",
                   {"name": conn.name, "error": error or "?"}, sources=[conn.service])


def _snapshot(conn: Connection, owner: int | None, dataset: dict, today: date) -> None:
    """One value per metric and day, for trend charts."""
    values: dict[str, float] = {}
    if conn.service == "invoiceninja":
        stats = nm.summary(dataset, today)
        values = {"revenue_ytd": stats["revenue_ytd"], "open_amount": stats["open_amount"]}
    elif conn.service == "kimai":
        values = {"month_min": km.summary(dataset, today)["month_min"]}
    if not values:
        return

    scope = f"{conn.id}:{owner or 0}"
    with session_scope() as s:
        for metric, value in values.items():
            data_repo.put_point(s, scope, metric, today.isoformat(), float(value))
