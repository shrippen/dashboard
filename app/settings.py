"""Operating settings from environment variables and Docker secrets.

Only values needed to run the container live here. Everything a user
configures (boards, connections, themes, OIDC mapping) lives in the database.
"""

from functools import lru_cache
from pathlib import Path

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict

SECRETS_DIR = Path("/run/secrets")
MASTER_KEY_FILE = "master_key"
DEV_MASTER_KEY_FILE = "dev_master_key"


class Settings(BaseSettings):
    model_config = SettingsConfigDict(env_prefix="", extra="ignore")

    base_url: str = "http://localhost:8080"
    data_dir: Path = Path("/data")
    database_url: str | None = None

    # Development: relaxed cookies, generated master key inside data_dir.
    dashboard_dev: bool = False
    dashboard_demo: bool = False
    dashboard_testing: bool = False

    master_key: str | None = None
    smtp_url: str | None = None
    smtp_from: str = "dashboard <dashboard@localhost>"
    smtp_password: str | None = None
    oidc_issuer: str | None = None
    oidc_client_id: str | None = None
    oidc_client_secret: str | None = None

    seed_file: Path | None = None
    scheduler_enabled: bool = True
    log_level: str = "INFO"

    session_idle_minutes: int = Field(default=60 * 24 * 7)
    session_absolute_hours: int = Field(default=24 * 30)
    oidc_session_hours: int = Field(default=12)

    @property
    def db_url(self) -> str:
        if self.database_url:
            return self.database_url

        return f"sqlite:///{self.data_dir / 'dashboard.db'}"

    @property
    def secure_cookies(self) -> bool:
        return self.base_url.startswith("https://")

    @property
    def icons_dir(self) -> Path:
        return self.data_dir / "icons"

    @property
    def themes_dir(self) -> Path:
        return self.data_dir / "themes"


def _read_secret(name: str) -> str | None:
    path = SECRETS_DIR / name
    if not path.is_file():
        return None

    return path.read_text().strip() or None


@lru_cache
def get_settings() -> Settings:
    settings = Settings()

    # Docker secrets win over plain environment variables.
    for name in ("master_key", "smtp_password", "oidc_client_secret"):
        value = _read_secret(name)
        if value:
            setattr(settings, name, value)

    return settings
