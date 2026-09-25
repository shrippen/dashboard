-- Fetch outcomes per connection and day: health view and daily budget.
CREATE TABLE conn_stats (
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    day TEXT NOT NULL,
    ok INTEGER NOT NULL DEFAULT 0,
    fail INTEGER NOT NULL DEFAULT 0,
    ms_sum INTEGER NOT NULL DEFAULT 0,
    last_ok_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (connection_id, day)
);

-- Token hygiene: when a secret was stored, when it expires ('' = unknown).
ALTER TABLE connections ADD COLUMN secret_at TEXT NOT NULL DEFAULT '';
ALTER TABLE connections ADD COLUMN secret_expires TEXT NOT NULL DEFAULT '';
ALTER TABLE user_credentials ADD COLUMN secret_at TEXT NOT NULL DEFAULT '';
UPDATE connections SET secret_at = created_at WHERE secret_enc IS NOT NULL;

-- Fetches per day for rate-limited APIs, 0 = unlimited.
ALTER TABLE connections ADD COLUMN daily_budget INTEGER NOT NULL DEFAULT 0;
