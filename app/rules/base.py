"""Rule contract: pure functions from datasets to findings.

    @rule("kimai.timer_running_long", ServiceType.KIMAI, hours=10)
    def timer(data, cfg, env) -> list[Finding]: ...

cfg  = defaults overridden by the space settings (settings["rules"][rule id])
env  = today, space settings, other datasets of the same space (cross rules)
Rules never do I/O; the analysis service feeds them.
"""

from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import date

from app.enums import ServiceType, Severity

CROSS = "cross"
DEADLINES = "deadlines"
ENABLED = "enabled"


@dataclass
class Finding:
    """One problem found by a rule, e.g. an overdue invoice."""

    fingerprint: str
    rule: str
    severity: Severity
    message: str
    params: dict = field(default_factory=dict)
    action_url: str | None = None
    action_label: str | None = None
    due: str | None = None
    sources: list[str] = field(default_factory=list)


@dataclass
class Env:
    today: date
    settings: dict
    datasets: dict[str, dict] = field(default_factory=dict)
    options: dict = field(default_factory=dict)


RuleFn = Callable[[dict, dict, Env], list[Finding]]


@dataclass(frozen=True)
class RuleSpec:
    id: str
    scope: str
    defaults: dict
    run: RuleFn


_registry: dict[str, RuleSpec] = {}


def rule(rule_id: str, scope: ServiceType | str, **defaults):
    def wrap(func: RuleFn) -> RuleFn:
        key = scope.value if isinstance(scope, ServiceType) else scope
        _registry[rule_id] = RuleSpec(rule_id, key, {ENABLED: True, **defaults}, func)
        return func

    return wrap


def for_scope(scope: str) -> list[RuleSpec]:
    return [spec for spec in _registry.values() if spec.scope == scope]


def all_rules() -> list[RuleSpec]:
    return sorted(_registry.values(), key=lambda r: r.id)


def config(spec: RuleSpec, settings: dict) -> dict:
    """Defaults, overridden per space: settings["rules"]["kimai.unbilled_hours"]["warn_days"]."""
    custom = (settings.get("rules") or {}).get(spec.id) or {}
    return {**spec.defaults, **{k: v for k, v in custom.items() if k in spec.defaults}}


def money(value: float, currency: str = "EUR") -> dict:
    """Typed parameter: formatted per reader locale."""
    return {"$money": round(value, 2), "currency": currency}


def day(value: str | date) -> dict:
    return {"$day": str(value)[:10]}


def num(value: float, digits: int = 0) -> dict:
    return {"$num": value, "digits": digits}
