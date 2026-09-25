"""Backup, key rotation, CLI import."""

import tarfile

from app.enums import CredentialMode, ServiceType
from app.services import connections, crypto, data, maintenance
from app.services.access import personal
from app.services.connections import Tls
from app.settings import get_settings
from tests.conftest import make_user, who
from tests.test_porting import DASHY


def test_backup(app, tmp_path):
    make_user("a@x.de")
    archive = maintenance.backup(get_settings(), tmp_path / "b")
    with tarfile.open(archive) as tar:
        assert "dashboard.db" in tar.getnames()


def test_rotate_key(app):
    uid = make_user("a@x.de")
    conn_id = connections.create(who(uid), personal(who(uid)).id, ServiceType.KIMAI, "K", "https://k.lan",
                                 CredentialMode.SHARED, "secret-token", Tls.VERIFY)
    assert maintenance.rotate_key("new-master") == 1

    crypto._master = crypto.master_from("new-master")
    conn = data.load_connection(conn_id)
    assert crypto.decrypt(conn.secret_enc, crypto.Purpose.CREDENTIAL) == "secret-token"


def test_cli_import(app, tmp_path):
    make_user("a@x.de")
    path = tmp_path / "conf.yml"
    path.write_text(DASHY)
    report = maintenance.import_file("a@x.de", path, "dashy")
    assert report.boards == 1
