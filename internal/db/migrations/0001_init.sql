-- Initial schema. dict/list columns are stored as JSON text; enums as their
-- string value (TEXT); booleans as INTEGER 0/1; timestamps as ISO-8601 TEXT.

CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    password_hash TEXT,
    role TEXT NOT NULL DEFAULT 'user',
    is_active INTEGER NOT NULL DEFAULT 1,
    is_breakglass INTEGER NOT NULL DEFAULT 0,
    locale TEXT NOT NULL DEFAULT 'de',
    color_mode TEXT NOT NULL DEFAULT 'auto',
    theme_id INTEGER REFERENCES themes(id) ON DELETE SET NULL,
    start_board_id INTEGER REFERENCES boards(id) ON DELETE SET NULL,
    search_engine TEXT,
    prefs TEXT NOT NULL DEFAULT '{}',
    totp_secret_enc BLOB,
    totp_enabled INTEGER NOT NULL DEFAULT 0,
    recovery_codes TEXT NOT NULL DEFAULT '[]',
    oidc_sub TEXT UNIQUE,
    created_at TEXT NOT NULL,
    last_login_at TEXT
);

CREATE TABLE teams (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE memberships (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    team_id INTEGER NOT NULL REFERENCES teams(id) ON DELETE CASCADE,
    role TEXT NOT NULL DEFAULT 'viewer',
    UNIQUE (user_id, team_id)
);

CREATE TABLE spaces (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL,
    name TEXT NOT NULL,
    owner_user_id INTEGER UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    team_id INTEGER UNIQUE REFERENCES teams(id) ON DELETE CASCADE,
    settings TEXT NOT NULL DEFAULT '{}',
    version INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE connections (
    id INTEGER PRIMARY KEY,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    name TEXT NOT NULL,
    service TEXT NOT NULL,
    url TEXT NOT NULL,
    credential_mode TEXT NOT NULL DEFAULT 'shared',
    secret_enc BLOB,
    options TEXT NOT NULL DEFAULT '{}',
    verify_tls INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    UNIQUE (space_id, key)
);

CREATE TABLE user_credentials (
    id INTEGER PRIMARY KEY,
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    secret_enc BLOB NOT NULL,
    UNIQUE (connection_id, user_id)
);

CREATE TABLE widgets (
    id INTEGER PRIMARY KEY,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    type TEXT NOT NULL,
    title TEXT NOT NULL DEFAULT '',
    config TEXT NOT NULL DEFAULT '{}',
    connection_id INTEGER REFERENCES connections(id) ON DELETE SET NULL,
    min_team_role TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    UNIQUE (space_id, key)
);

CREATE TABLE boards (
    id INTEGER PRIMARY KEY,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    position INTEGER NOT NULL DEFAULT 0,
    theme_id INTEGER REFERENCES themes(id) ON DELETE SET NULL,
    is_template INTEGER NOT NULL DEFAULT 0,
    min_team_role TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    UNIQUE (space_id, slug)
);

CREATE TABLE sections (
    id INTEGER PRIMARY KEY,
    board_id INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    position INTEGER NOT NULL DEFAULT 0,
    cols INTEGER,
    size TEXT NOT NULL DEFAULT 'medium',
    sort TEXT NOT NULL DEFAULT 'manual',
    collapsed INTEGER NOT NULL DEFAULT 0,
    area TEXT NOT NULL DEFAULT 'main'
);

CREATE TABLE placements (
    id INTEGER PRIMARY KEY,
    section_id INTEGER NOT NULL REFERENCES sections(id) ON DELETE CASCADE,
    widget_id INTEGER NOT NULL REFERENCES widgets(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE overlays (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    board_id INTEGER NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    data TEXT NOT NULL DEFAULT '{}',
    UNIQUE (user_id, board_id)
);

CREATE TABLE shares (
    id INTEGER PRIMARY KEY,
    resource_kind TEXT NOT NULL,
    resource_id INTEGER NOT NULL,
    grantee_kind TEXT NOT NULL,
    grantee_id INTEGER NOT NULL,
    right INTEGER NOT NULL,
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    UNIQUE (resource_kind, resource_id, grantee_kind, grantee_id)
);

CREATE TABLE revisions (
    id INTEGER PRIMARY KEY,
    kind TEXT NOT NULL,
    entity_id INTEGER NOT NULL,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    version INTEGER NOT NULL,
    data TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX ix_revisions_entity_id ON revisions(entity_id);

CREATE TABLE themes (
    id INTEGER PRIMARY KEY,
    space_id INTEGER REFERENCES spaces(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    builtin INTEGER NOT NULL DEFAULT 0,
    contract INTEGER NOT NULL DEFAULT 1,
    dark TEXT NOT NULL DEFAULT '{}',
    light TEXT NOT NULL DEFAULT '{}',
    custom_css TEXT NOT NULL DEFAULT '',
    fonts TEXT NOT NULL DEFAULT '[]',
    digest TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    UNIQUE (space_id, slug)
);

CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    token_hash TEXT NOT NULL UNIQUE,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    method TEXT NOT NULL,
    csrf TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    ip TEXT NOT NULL DEFAULT '',
    user_agent TEXT NOT NULL DEFAULT '',
    pending_2fa INTEGER NOT NULL DEFAULT 0,
    id_token TEXT
);

CREATE TABLE api_tokens (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    prefix TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT 'read',
    board_ids TEXT NOT NULL DEFAULT '[]',
    expires_at TEXT,
    last_used_at TEXT,
    created_at TEXT NOT NULL
);

CREATE TABLE invites (
    id INTEGER PRIMARY KEY,
    email TEXT NOT NULL,
    token_hash TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL DEFAULT 'user',
    teams TEXT NOT NULL DEFAULT '[]',
    created_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    expires_at TEXT NOT NULL,
    used_at TEXT
);

CREATE TABLE reset_tokens (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TEXT NOT NULL,
    used_at TEXT
);

CREATE TABLE audit_log (
    id INTEGER PRIMARY KEY,
    at TEXT NOT NULL,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target TEXT NOT NULL DEFAULT '',
    detail TEXT NOT NULL DEFAULT '{}',
    ip TEXT NOT NULL DEFAULT ''
);
CREATE INDEX ix_audit_log_at ON audit_log(at);

CREATE TABLE instance_settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE cache (
    key TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    ok_at TEXT,
    data TEXT,
    error TEXT
);

CREATE TABLE metric_points (
    id INTEGER PRIMARY KEY,
    scope TEXT NOT NULL,
    metric TEXT NOT NULL,
    day TEXT NOT NULL,
    value REAL NOT NULL,
    UNIQUE (scope, metric, day)
);

CREATE TABLE hints (
    id INTEGER PRIMARY KEY,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    fingerprint TEXT NOT NULL,
    rule TEXT NOT NULL,
    severity INTEGER NOT NULL,
    message TEXT NOT NULL,
    params TEXT NOT NULL DEFAULT '{}',
    action_url TEXT,
    action_label TEXT,
    due TEXT,
    sources TEXT NOT NULL DEFAULT '[]',
    connection_id INTEGER REFERENCES connections(id) ON DELETE CASCADE,
    first_seen TEXT NOT NULL,
    last_seen TEXT NOT NULL,
    resolved_at TEXT,
    UNIQUE (space_id, user_id, fingerprint)
);

CREATE TABLE hint_marks (
    id INTEGER PRIMARY KEY,
    hint_id INTEGER NOT NULL REFERENCES hints(id) ON DELETE CASCADE,
    user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    state TEXT NOT NULL,
    until TEXT,
    at TEXT NOT NULL,
    UNIQUE (hint_id, user_id)
);

CREATE TABLE notify_channels (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    url_enc BLOB NOT NULL,
    min_severity INTEGER NOT NULL DEFAULT 20,
    enabled INTEGER NOT NULL DEFAULT 1
);

CREATE TABLE notify_log (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    hint_id INTEGER NOT NULL REFERENCES hints(id) ON DELETE CASCADE,
    sent_at TEXT NOT NULL,
    UNIQUE (user_id, hint_id)
);
