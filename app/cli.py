"""Command line: dashboard backup | rotate-key | import.

    docker compose exec dashboard dashboard backup /data/backups
    docker compose exec dashboard dashboard rotate-key /run/secrets/new_master_key
    docker compose exec dashboard dashboard import --email me@example.org --dashy /data/conf.yml
"""

import argparse
import sys
from pathlib import Path

from app.db.base import init_engine
from app.services import crypto, maintenance, system
from app.settings import get_settings


def _prepare():
    settings = get_settings()
    init_engine(settings.db_url)
    system.migrate(settings.db_url)
    crypto.init_crypto(settings)
    return settings


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="dashboard")
    sub = parser.add_subparsers(dest="command", required=True)

    backup = sub.add_parser("backup", help="SQLite + icons as tar.gz")
    backup.add_argument("target", type=Path)

    rotate = sub.add_parser("rotate-key", help="re-encrypt secrets with a new master key")
    rotate.add_argument("new_key_file", type=Path)

    load = sub.add_parser("import", help="import YAML or Dashy conf.yml into a personal space")
    load.add_argument("--email", required=True)
    load.add_argument("--dashy", action="store_true")
    load.add_argument("file", type=Path)

    args = parser.parse_args(argv)
    settings = _prepare()

    if args.command == "backup":
        print(maintenance.backup(settings, args.target))
        return 0
    if args.command == "rotate-key":
        count = maintenance.rotate_key(args.new_key_file.read_text().strip())
        print(f"{count} secrets re-encrypted. Replace the master_key secret and restart.")
        return 0

    report = maintenance.import_file(args.email, args.file, "dashy" if args.dashy else "yaml")
    print(f"boards {report.boards}, widgets {report.widgets}, connections {report.connections}")
    for line in report.skipped + report.notes:
        print(f"  {line}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
