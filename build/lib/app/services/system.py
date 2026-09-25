"""Start-up and instance-wide settings.

    start()
      ├─ database: engine + migrations
      ├─ crypto: master key
      ├─ instance space, builtin theme
      ├─ egress guard from network policy
      ├─ seed.yml / demo data (first start only)
      └─ setup code in the log while no user exists
"""

import ipaddress
import logging
from dataclasses import dataclass, field
from enum import StrEnum
from pathlib import Path

from alembic import command
from alembic.config import Config

from app.db.base import init_engine, session_scope
from app.db.models import Space
from app.drivers import http
from app.enums import SpaceKind
from app.repos import content, misc
from app.services import auth, crypto, seed, themes
from app.services.access import AccessDenied, Principal
from app.settings import Settings

log = logging.getLogger(__name__)

MIGRATIONS = Path(__file__).resolve().parent.parent / "db" / "migrations"
NETWORK_KEY = "network"
IFRAME_KEY = "iframe"
SECURITY_KEY = "security"
REGISTRATION_KEY = "registration"
INSTANCE_NAME = "Instanz"


class NetMode(StrEnum):
    OPEN = "open"
    ALLOWLIST = "allowlist"


@dataclass
class NetworkPolicy:
    mode: NetMode = NetMode.OPEN
    networks: list[str] = field(default_factory=list)
    hosts: list[str] = field(default_factory=list)
    public: bool = True


def migrate(url: str) -> None:
    config = Config()
    config.set_main_option("script_location", str(MIGRATIONS))
    config.set_main_option("sqlalchemy.url", url)
    command.upgrade(config, "head")


def start(settings: Settings) -> None:
    settings.data_dir.mkdir(parents=True, exist_ok=True)
    settings.icons_dir.mkdir(parents=True, exist_ok=True)
    init_engine(settings.db_url)
    migrate(settings.db_url)
    crypto.init_crypto(settings)

    with session_scope() as s:
        if content.instance_space(s) is None:
            content.add(s, Space(kind=SpaceKind.INSTANCE, name=INSTANCE_NAME))

    themes.ensure_builtin()
    apply_network(network())
    _seed(settings)
    auth.ensure_setup_code()


def _seed(settings: Settings) -> None:
    if settings.dashboard_demo:
        seed.demo()
    if settings.seed_file and settings.seed_file.is_file():
        seed.from_file(settings.seed_file)


# ── Network policy ──


def network() -> NetworkPolicy:
    with session_scope() as s:
        raw = misc.setting(s, NETWORK_KEY)
    return NetworkPolicy(
        mode=NetMode(raw.get("mode", NetMode.OPEN)),
        networks=list(raw.get("networks", [])),
        hosts=list(raw.get("hosts", [])),
        public=bool(raw.get("public", True)),
    )


def apply_network(policy: NetworkPolicy) -> None:
    if policy.mode == NetMode.OPEN:
        http.set_guard(None)
        return

    nets = [ipaddress.ip_network(n, strict=False) for n in policy.networks]
    hosts = {h.lower() for h in policy.hosts}

    def guard(host: str, addresses: list) -> bool:
        if host.lower() in hosts:
            return True
        for address in addresses:
            allowed = any(address in net for net in nets)
            if policy.public and address.is_global:
                allowed = True
            if not allowed:
                return False
        return True

    http.set_guard(guard)


def set_network(who: Principal, policy: NetworkPolicy) -> None:
    if not who.is_admin:
        raise AccessDenied("network")
    for net in policy.networks:
        ipaddress.ip_network(net, strict=False)

    with session_scope() as s:
        misc.set_setting(
            s,
            NETWORK_KEY,
            {"mode": policy.mode.value, "networks": policy.networks, "hosts": policy.hosts,
             "public": policy.public},
        )
    apply_network(policy)


# ── Generic instance settings (admin) ──


def get_setting(key: str) -> dict:
    with session_scope() as s:
        return misc.setting(s, key)


def put_setting(who: Principal, key: str, value: dict) -> None:
    if not who.is_admin:
        raise AccessDenied(key)
    with session_scope() as s:
        misc.set_setting(s, key, value)


def iframe_origins() -> list[str]:
    return list(get_setting(IFRAME_KEY).get("origins", []))
