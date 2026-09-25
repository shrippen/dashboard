"""Operator tasks: backup, master key rotation, command-line import."""

import base64
import sqlite3
import tarfile
from datetime import datetime
from pathlib import Path

from app.db.base import session_scope
from app.repos import content, misc, users
from app.services import access, crypto, porting
from app.services.crypto import Purpose
from app.services.porting import Report
from app.settings import Settings

OIDC_KEY = "oidc"
SQLITE_PREFIX = "sqlite:///"


class MaintenanceError(Exception):
    pass


def backup(settings: Settings, target_dir: Path) -> Path:
    """Consistent SQLite copy (online backup API) plus icons, packed as tar.gz."""
    if not settings.db_url.startswith(SQLITE_PREFIX):
        raise MaintenanceError("backup supports SQLite only; use pg_dump for other databases")

    stamp = datetime.now().strftime("%Y%m%d-%H%M%S")
    target_dir.mkdir(parents=True, exist_ok=True)
    copy = target_dir / f"dashboard-{stamp}.db"
    source = sqlite3.connect(settings.db_url[len(SQLITE_PREFIX):])
    dest = sqlite3.connect(copy)
    with dest:
        source.backup(dest)
    source.close()
    dest.close()

    archive = target_dir / f"dashboard-{stamp}.tar.gz"
    with tarfile.open(archive, "w:gz") as tar:
        tar.add(copy, arcname="dashboard.db")
        if settings.icons_dir.exists():
            tar.add(settings.icons_dir, arcname="icons")
    copy.unlink()
    return archive


def rotate_key(new_secret: str) -> int:
    """Re-encrypt every secret with a new master key. Returns the number of values."""
    new = crypto.master_from(new_secret)
    count = 0

    def swap(blob: bytes | None, purpose: Purpose) -> bytes | None:
        nonlocal count
        if not blob:
            return blob
        count += 1
        return crypto.encrypt(crypto.decrypt(blob, purpose), purpose, new)

    with session_scope() as s:
        rows = misc.encrypted_rows(s)
        for conn in rows["connections"]:
            conn.secret_enc = swap(conn.secret_enc, Purpose.CREDENTIAL)
        for cred in rows["credentials"]:
            cred.secret_enc = swap(cred.secret_enc, Purpose.CREDENTIAL)
        for user in rows["users"]:
            user.totp_secret_enc = swap(user.totp_secret_enc, Purpose.TOTP)
        for channel in rows["channels"]:
            channel.url_enc = swap(channel.url_enc, Purpose.NOTIFY)
        raw = misc.setting(s, OIDC_KEY)
        if raw.get("secret_enc"):
            blob = swap(base64.b64decode(raw["secret_enc"]), Purpose.SETTING)
            raw["secret_enc"] = base64.b64encode(blob).decode()
            misc.set_setting(s, OIDC_KEY, raw)
    return count


def import_file(email: str, path: Path, kind: str) -> Report:
    """Import YAML or a Dashy conf.yml into the personal space of a user."""
    with session_scope() as s:
        user = users.by_email(s, email)
        if user is None:
            raise MaintenanceError(f"no user {email}")
        who = access.principal(s, user.id)
        space_id = content.personal_space(s, user.id).id

    text = path.read_text(encoding="utf-8")
    if kind == "dashy":
        return porting.import_dashy(who, space_id, text)
    return porting.import_space(who, space_id, text, porting.ImportMode.MERGE)
