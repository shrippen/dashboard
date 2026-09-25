"""First-start content: optional seed.yml and the demo instance."""

import logging
from pathlib import Path

from app.db.base import session_scope
from app.enums import InstanceRole, Locale
from app.repos import content, users
from app.services import access, accounts, analysis, porting
from app.services.porting import ImportMode

log = logging.getLogger(__name__)

DEMO_ADMIN = "admin@demo.local"
DEMO_USER = "alex@demo.local"
DEMO_PASSWORD = "demo-password-1"
DEMO_FILE = Path(__file__).resolve().parent.parent / "demo" / "instance.yml"
DEMO_PERSONAL = Path(__file__).resolve().parent.parent / "demo" / "personal.yml"


def _instance_empty() -> bool:
    with session_scope() as s:
        space = content.instance_space(s)
        return not content.boards(s, [space.id])


def from_file(path: Path) -> None:
    """Import seed.yml into the instance space once (only while it has no boards)."""
    if not _instance_empty():
        return

    with session_scope() as s:
        admin = next((u for u in users.all_users(s) if u.role == InstanceRole.ADMIN), None)
        if admin is None:
            log.warning("seed.yml waits for the first admin")
            return
        who = access.principal(s, admin.id)
        space_id = content.instance_space(s).id

    report = porting.import_space(who, space_id, path.read_text(), ImportMode.MERGE)
    log.info("seed imported: %s boards, %s widgets", report.boards, report.widgets)


def demo() -> None:
    """Demo users and boards with made-up data (connections use demo:// URLs)."""
    with session_scope() as s:
        if users.count(s) > 0:
            return
        admin = accounts.create(s, DEMO_ADMIN, "Admin", DEMO_PASSWORD, InstanceRole.ADMIN, Locale.DE)
        admin.is_breakglass = True
        user = accounts.create(s, DEMO_USER, "Alex", DEMO_PASSWORD, InstanceRole.USER, Locale.DE)
        accounts.join_teams(s, user.id, [{"team": "IT", "role": "editor"}])
        accounts.join_teams(s, admin.id, [{"team": "IT", "role": "owner"}])
        admin_id, user_id = admin.id, user.id

    with session_scope() as s:
        admin_who = access.principal(s, admin_id)
        user_who = access.principal(s, user_id)
        instance_id = content.instance_space(s).id
        personal_id = content.personal_space(s, user_id).id

    porting.import_space(admin_who, instance_id, DEMO_FILE.read_text(), ImportMode.MERGE)
    porting.import_space(user_who, personal_id, DEMO_PERSONAL.read_text(), ImportMode.MERGE)
    analysis.run_all()
    log.warning("DEMO MODE: %s / %s and %s / %s", DEMO_ADMIN, DEMO_PASSWORD, DEMO_USER, DEMO_PASSWORD)
