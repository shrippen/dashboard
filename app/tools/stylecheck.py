"""Fail if CSS outside themes/ and vendor/ uses fixed colours instead of tokens."""

import re
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
CHECKED = [ROOT / "static" / "dashboard.css"]
COLOR = re.compile(r"#[0-9a-fA-F]{3,8}\b|rgba?\(|hsla?\(")


def main() -> int:
    bad = []
    for path in CHECKED:
        for number, line in enumerate(path.read_text().splitlines(), 1):
            if COLOR.search(line):
                bad.append(f"{path.name}:{number}: {line.strip()[:80]}")

    for item in bad:
        print(item)
    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main())
