"""ORM models.

    User ──Membership(role)──► Team
      │                          │
      └── Space(personal)        └── Space(team)          Space(instance)
             │
             ├── Connection ── UserCredential (personal tokens)
             ├── Widget ◄── Placement ── Section ── Board ◄── Overlay (per user)
             ├── Theme
             └── Hint ── HintMark (per user or team-wide)

    Share: resource → user|team with a Right.
"""

from datetime import datetime

from sqlalchemy import (
    Boolean,
    Enum,
    Float,
    ForeignKey,
    Integer,
    LargeBinary,
    String,
    Text,
    UniqueConstraint,
)
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base, utcnow
from app.enums import (
    AuthMethod,
    ColorMode,
    CredentialMode,
    GranteeKind,
    HintState,
    InstanceRole,
    Locale,
    ResourceKind,
    RevisionKind,
    Right,
    SortOrder,
    SpaceKind,
    TeamRole,
    TileSize,
    TokenScope,
)

NAME_LEN = 200
URL_LEN = 2000
KEY_LEN = 120


def _enum(cls) -> Enum:
    return Enum(cls, native_enum=False, length=40, values_callable=lambda e: [m.value for m in e])


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True)
    email: Mapped[str] = mapped_column(String(NAME_LEN), unique=True)
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    password_hash: Mapped[str | None] = mapped_column(String(255))
    role: Mapped[InstanceRole] = mapped_column(_enum(InstanceRole), default=InstanceRole.USER)
    is_active: Mapped[bool] = mapped_column(Boolean, default=True)
    is_breakglass: Mapped[bool] = mapped_column(Boolean, default=False)

    locale: Mapped[Locale] = mapped_column(_enum(Locale), default=Locale.DE)
    color_mode: Mapped[ColorMode] = mapped_column(_enum(ColorMode), default=ColorMode.AUTO)
    theme_id: Mapped[int | None] = mapped_column(
        ForeignKey("themes.id", ondelete="SET NULL", use_alter=True)
    )
    start_board_id: Mapped[int | None] = mapped_column(
        ForeignKey("boards.id", ondelete="SET NULL", use_alter=True)
    )
    search_engine: Mapped[str | None] = mapped_column(String(URL_LEN))
    prefs: Mapped[dict] = mapped_column(default=dict)

    totp_secret_enc: Mapped[bytes | None] = mapped_column(LargeBinary)
    totp_enabled: Mapped[bool] = mapped_column(Boolean, default=False)
    recovery_codes: Mapped[list] = mapped_column(default=list)

    oidc_sub: Mapped[str | None] = mapped_column(String(255), unique=True)
    created_at: Mapped[datetime] = mapped_column(default=utcnow)
    last_login_at: Mapped[datetime | None]

    memberships: Mapped[list["Membership"]] = relationship(
        back_populates="user", cascade="all, delete-orphan"
    )


class Team(Base):
    __tablename__ = "teams"

    id: Mapped[int] = mapped_column(primary_key=True)
    name: Mapped[str] = mapped_column(String(NAME_LEN), unique=True)
    created_at: Mapped[datetime] = mapped_column(default=utcnow)

    memberships: Mapped[list["Membership"]] = relationship(
        back_populates="team", cascade="all, delete-orphan"
    )


class Membership(Base):
    __tablename__ = "memberships"
    __table_args__ = (UniqueConstraint("user_id", "team_id"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    team_id: Mapped[int] = mapped_column(ForeignKey("teams.id", ondelete="CASCADE"))
    role: Mapped[TeamRole] = mapped_column(_enum(TeamRole), default=TeamRole.VIEWER)

    user: Mapped[User] = relationship(back_populates="memberships")
    team: Mapped[Team] = relationship(back_populates="memberships")


class Space(Base):
    __tablename__ = "spaces"

    id: Mapped[int] = mapped_column(primary_key=True)
    kind: Mapped[SpaceKind] = mapped_column(_enum(SpaceKind))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    owner_user_id: Mapped[int | None] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), unique=True
    )
    team_id: Mapped[int | None] = mapped_column(
        ForeignKey("teams.id", ondelete="CASCADE"), unique=True
    )
    settings: Mapped[dict] = mapped_column(default=dict)
    version: Mapped[int] = mapped_column(Integer, default=1)


class Connection(Base):
    __tablename__ = "connections"

    id: Mapped[int] = mapped_column(primary_key=True)
    space_id: Mapped[int] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    key: Mapped[str] = mapped_column(String(KEY_LEN))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    service: Mapped[str] = mapped_column(String(40))
    url: Mapped[str] = mapped_column(String(URL_LEN))
    credential_mode: Mapped[CredentialMode] = mapped_column(
        _enum(CredentialMode), default=CredentialMode.SHARED
    )
    secret_enc: Mapped[bytes | None] = mapped_column(LargeBinary)
    options: Mapped[dict] = mapped_column(default=dict)
    verify_tls: Mapped[bool] = mapped_column(Boolean, default=True)
    created_at: Mapped[datetime] = mapped_column(default=utcnow)

    __table_args__ = (UniqueConstraint("space_id", "key"),)


class UserCredential(Base):
    __tablename__ = "user_credentials"
    __table_args__ = (UniqueConstraint("connection_id", "user_id"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    connection_id: Mapped[int] = mapped_column(ForeignKey("connections.id", ondelete="CASCADE"))
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    secret_enc: Mapped[bytes] = mapped_column(LargeBinary)


class Widget(Base):
    __tablename__ = "widgets"
    __table_args__ = (UniqueConstraint("space_id", "key"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    space_id: Mapped[int] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    key: Mapped[str] = mapped_column(String(KEY_LEN))
    type: Mapped[str] = mapped_column(String(40))
    title: Mapped[str] = mapped_column(String(NAME_LEN), default="")
    config: Mapped[dict] = mapped_column(default=dict)
    connection_id: Mapped[int | None] = mapped_column(
        ForeignKey("connections.id", ondelete="SET NULL")
    )
    # Team members below this role do not see the widget (unless shared).
    min_team_role: Mapped[TeamRole | None] = mapped_column(_enum(TeamRole))
    version: Mapped[int] = mapped_column(Integer, default=1)
    updated_at: Mapped[datetime] = mapped_column(default=utcnow, onupdate=utcnow)


class Board(Base):
    __tablename__ = "boards"
    __table_args__ = (UniqueConstraint("space_id", "slug"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    space_id: Mapped[int] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    slug: Mapped[str] = mapped_column(String(KEY_LEN))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    position: Mapped[int] = mapped_column(Integer, default=0)
    theme_id: Mapped[int | None] = mapped_column(ForeignKey("themes.id", ondelete="SET NULL"))
    is_template: Mapped[bool] = mapped_column(Boolean, default=False)
    min_team_role: Mapped[TeamRole | None] = mapped_column(_enum(TeamRole))
    version: Mapped[int] = mapped_column(Integer, default=1)
    updated_at: Mapped[datetime] = mapped_column(default=utcnow, onupdate=utcnow)

    sections: Mapped[list["Section"]] = relationship(
        back_populates="board", cascade="all, delete-orphan", order_by="Section.position"
    )


class Section(Base):
    __tablename__ = "sections"

    id: Mapped[int] = mapped_column(primary_key=True)
    board_id: Mapped[int] = mapped_column(ForeignKey("boards.id", ondelete="CASCADE"))
    title: Mapped[str] = mapped_column(String(NAME_LEN), default="")
    position: Mapped[int] = mapped_column(Integer, default=0)
    cols: Mapped[int | None] = mapped_column(Integer)
    size: Mapped[TileSize] = mapped_column(_enum(TileSize), default=TileSize.MEDIUM)
    sort: Mapped[SortOrder] = mapped_column(_enum(SortOrder), default=SortOrder.MANUAL)
    collapsed: Mapped[bool] = mapped_column(Boolean, default=False)
    # Layout column on wide screens: main or side.
    area: Mapped[str] = mapped_column(String(20), default="main")

    board: Mapped[Board] = relationship(back_populates="sections")
    placements: Mapped[list["Placement"]] = relationship(
        back_populates="section", cascade="all, delete-orphan", order_by="Placement.position"
    )


class Placement(Base):
    __tablename__ = "placements"

    id: Mapped[int] = mapped_column(primary_key=True)
    section_id: Mapped[int] = mapped_column(ForeignKey("sections.id", ondelete="CASCADE"))
    widget_id: Mapped[int] = mapped_column(ForeignKey("widgets.id", ondelete="CASCADE"))
    position: Mapped[int] = mapped_column(Integer, default=0)

    section: Mapped[Section] = relationship(back_populates="placements")
    widget: Mapped[Widget] = relationship()


class Overlay(Base):
    """Personal layout changes of one user on a board he may not edit."""

    __tablename__ = "overlays"
    __table_args__ = (UniqueConstraint("user_id", "board_id"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    board_id: Mapped[int] = mapped_column(ForeignKey("boards.id", ondelete="CASCADE"))
    data: Mapped[dict] = mapped_column(default=dict)


class Share(Base):
    __tablename__ = "shares"
    __table_args__ = (
        UniqueConstraint("resource_kind", "resource_id", "grantee_kind", "grantee_id"),
    )

    id: Mapped[int] = mapped_column(primary_key=True)
    resource_kind: Mapped[ResourceKind] = mapped_column(_enum(ResourceKind))
    resource_id: Mapped[int] = mapped_column(Integer)
    grantee_kind: Mapped[GranteeKind] = mapped_column(_enum(GranteeKind))
    grantee_id: Mapped[int] = mapped_column(Integer)
    right: Mapped[Right] = mapped_column(Integer)
    created_by: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))


class Revision(Base):
    __tablename__ = "revisions"

    id: Mapped[int] = mapped_column(primary_key=True)
    kind: Mapped[RevisionKind] = mapped_column(_enum(RevisionKind))
    entity_id: Mapped[int] = mapped_column(Integer, index=True)
    space_id: Mapped[int] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    user_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    version: Mapped[int] = mapped_column(Integer)
    data: Mapped[dict] = mapped_column(default=dict)
    created_at: Mapped[datetime] = mapped_column(default=utcnow)


class Theme(Base):
    __tablename__ = "themes"
    __table_args__ = (UniqueConstraint("space_id", "slug"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    space_id: Mapped[int | None] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    slug: Mapped[str] = mapped_column(String(KEY_LEN))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    builtin: Mapped[bool] = mapped_column(Boolean, default=False)
    contract: Mapped[int] = mapped_column(Integer, default=1)
    dark: Mapped[dict] = mapped_column(default=dict)
    light: Mapped[dict] = mapped_column(default=dict)
    custom_css: Mapped[str] = mapped_column(Text, default="")
    fonts: Mapped[list] = mapped_column(default=list)
    # Builtin themes: hash of the shipped tokens, to refresh after updates.
    digest: Mapped[str | None] = mapped_column(String(64))
    version: Mapped[int] = mapped_column(Integer, default=1)
    updated_at: Mapped[datetime] = mapped_column(default=utcnow, onupdate=utcnow)


class LoginSession(Base):
    __tablename__ = "sessions"

    id: Mapped[int] = mapped_column(primary_key=True)
    token_hash: Mapped[str] = mapped_column(String(64), unique=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    method: Mapped[AuthMethod] = mapped_column(_enum(AuthMethod))
    csrf: Mapped[str] = mapped_column(String(64))
    created_at: Mapped[datetime] = mapped_column(default=utcnow)
    last_seen: Mapped[datetime] = mapped_column(default=utcnow)
    expires_at: Mapped[datetime]
    ip: Mapped[str] = mapped_column(String(64), default="")
    user_agent: Mapped[str] = mapped_column(String(300), default="")
    # Second factor still pending: session may only reach /login/totp.
    pending_2fa: Mapped[bool] = mapped_column(Boolean, default=False)
    id_token: Mapped[str | None] = mapped_column(Text)


class ApiToken(Base):
    __tablename__ = "api_tokens"

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    token_hash: Mapped[str] = mapped_column(String(64), unique=True)
    prefix: Mapped[str] = mapped_column(String(12))
    scope: Mapped[TokenScope] = mapped_column(_enum(TokenScope), default=TokenScope.READ)
    board_ids: Mapped[list] = mapped_column(default=list)
    expires_at: Mapped[datetime | None]
    last_used_at: Mapped[datetime | None]
    created_at: Mapped[datetime] = mapped_column(default=utcnow)


class Invite(Base):
    __tablename__ = "invites"

    id: Mapped[int] = mapped_column(primary_key=True)
    email: Mapped[str] = mapped_column(String(NAME_LEN))
    token_hash: Mapped[str] = mapped_column(String(64), unique=True)
    role: Mapped[InstanceRole] = mapped_column(_enum(InstanceRole), default=InstanceRole.USER)
    teams: Mapped[list] = mapped_column(default=list)
    created_by: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    expires_at: Mapped[datetime]
    used_at: Mapped[datetime | None]


class ResetToken(Base):
    __tablename__ = "reset_tokens"

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    token_hash: Mapped[str] = mapped_column(String(64), unique=True)
    expires_at: Mapped[datetime]
    used_at: Mapped[datetime | None]


class AuditEntry(Base):
    __tablename__ = "audit_log"

    id: Mapped[int] = mapped_column(primary_key=True)
    at: Mapped[datetime] = mapped_column(default=utcnow, index=True)
    user_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="SET NULL"))
    action: Mapped[str] = mapped_column(String(80))
    target: Mapped[str] = mapped_column(String(200), default="")
    detail: Mapped[dict] = mapped_column(default=dict)
    ip: Mapped[str] = mapped_column(String(64), default="")


class InstanceSetting(Base):
    __tablename__ = "instance_settings"

    key: Mapped[str] = mapped_column(String(KEY_LEN), primary_key=True)
    value: Mapped[dict] = mapped_column(default=dict)


class CacheEntry(Base):
    """Last result of one source query. Key: source + connection + credential owner + params."""

    __tablename__ = "cache"

    key: Mapped[str] = mapped_column(String(64), primary_key=True)
    source: Mapped[str] = mapped_column(String(60))
    fetched_at: Mapped[datetime] = mapped_column(default=utcnow)
    ok_at: Mapped[datetime | None]
    data: Mapped[dict | None]
    error: Mapped[str | None] = mapped_column(Text)


class MetricPoint(Base):
    """Daily value of one metric, for trends."""

    __tablename__ = "metric_points"
    __table_args__ = (UniqueConstraint("scope", "metric", "day"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    scope: Mapped[str] = mapped_column(String(120))
    metric: Mapped[str] = mapped_column(String(80))
    day: Mapped[str] = mapped_column(String(10))
    value: Mapped[float] = mapped_column(Float)


class Hint(Base):
    __tablename__ = "hints"
    __table_args__ = (UniqueConstraint("space_id", "user_id", "fingerprint"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    space_id: Mapped[int] = mapped_column(ForeignKey("spaces.id", ondelete="CASCADE"))
    # Set when the hint stems from personal credentials: only that user sees it.
    user_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    fingerprint: Mapped[str] = mapped_column(String(200))
    rule: Mapped[str] = mapped_column(String(80))
    severity: Mapped[int] = mapped_column(Integer)
    message: Mapped[str] = mapped_column(String(120))
    params: Mapped[dict] = mapped_column(default=dict)
    action_url: Mapped[str | None] = mapped_column(String(URL_LEN))
    action_label: Mapped[str | None] = mapped_column(String(120))
    due: Mapped[str | None] = mapped_column(String(10))
    sources: Mapped[list] = mapped_column(default=list)
    connection_id: Mapped[int | None] = mapped_column(
        ForeignKey("connections.id", ondelete="CASCADE")
    )
    first_seen: Mapped[datetime] = mapped_column(default=utcnow)
    last_seen: Mapped[datetime] = mapped_column(default=utcnow)
    resolved_at: Mapped[datetime | None]


class HintMark(Base):
    """Acknowledge/snooze. user_id NULL = team-wide."""

    __tablename__ = "hint_marks"
    __table_args__ = (UniqueConstraint("hint_id", "user_id"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    hint_id: Mapped[int] = mapped_column(ForeignKey("hints.id", ondelete="CASCADE"))
    user_id: Mapped[int | None] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    state: Mapped[HintState] = mapped_column(_enum(HintState))
    until: Mapped[datetime | None]
    at: Mapped[datetime] = mapped_column(default=utcnow)


class NotifyChannel(Base):
    __tablename__ = "notify_channels"

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    name: Mapped[str] = mapped_column(String(NAME_LEN))
    url_enc: Mapped[bytes] = mapped_column(LargeBinary)
    min_severity: Mapped[int] = mapped_column(Integer, default=20)
    enabled: Mapped[bool] = mapped_column(Boolean, default=True)


class NotifyLog(Base):
    __tablename__ = "notify_log"
    __table_args__ = (UniqueConstraint("user_id", "hint_id"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"))
    hint_id: Mapped[int] = mapped_column(ForeignKey("hints.id", ondelete="CASCADE"))
    sent_at: Mapped[datetime] = mapped_column(default=utcnow)
