"""Domain enums shared across layers."""

from enum import IntEnum, StrEnum


class InstanceRole(StrEnum):
    ADMIN = "admin"
    USER = "user"


class TeamRole(StrEnum):
    OWNER = "owner"
    EDITOR = "editor"
    VIEWER = "viewer"


class SpaceKind(StrEnum):
    PERSONAL = "personal"
    TEAM = "team"
    INSTANCE = "instance"


class Right(IntEnum):
    """Ordered: a higher right includes all lower ones."""

    NONE = 0
    VIEW = 10
    USE = 20
    EDIT = 30
    MANAGE = 40


class ResourceKind(StrEnum):
    SPACE = "space"
    BOARD = "board"
    WIDGET = "widget"
    CONNECTION = "connection"
    THEME = "theme"


class GranteeKind(StrEnum):
    USER = "user"
    TEAM = "team"


class CredentialMode(StrEnum):
    SHARED = "shared"
    PERSONAL = "personal"


class ServiceType(StrEnum):
    KIMAI = "kimai"
    INVOICENINJA = "invoiceninja"
    SNIPEIT = "snipeit"
    DAWARICH = "dawarich"
    GLANCES = "glances"


class Severity(IntEnum):
    INFO = 10
    WARN = 20
    CRITICAL = 30


class HintState(StrEnum):
    OPEN = "open"
    SNOOZED = "snoozed"
    ACKNOWLEDGED = "acknowledged"
    RESOLVED = "resolved"


class HintAckMode(StrEnum):
    """Whether acknowledging a team hint applies to the team or to one user."""

    PER_USER = "per_user"
    TEAM = "team"


class AuthMethod(StrEnum):
    PASSWORD = "password"
    OIDC = "oidc"
    TOKEN = "token"


class ColorMode(StrEnum):
    AUTO = "auto"
    DARK = "dark"
    LIGHT = "light"


class Locale(StrEnum):
    DE = "de"
    EN = "en"


class TokenScope(StrEnum):
    READ = "read"
    EMBED = "embed"


class TileSize(StrEnum):
    SMALL = "small"
    MEDIUM = "medium"
    LARGE = "large"


class SortOrder(StrEnum):
    MANUAL = "manual"
    ALPHABETICAL = "alphabetical"


class LinkTarget(StrEnum):
    NEW_TAB = "newtab"
    SAME_TAB = "sametab"


class RevisionKind(StrEnum):
    BOARD = "board"
    WIDGET = "widget"
    SPACE = "space"
    THEME = "theme"
