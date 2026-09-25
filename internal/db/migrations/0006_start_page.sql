-- Link clicks per user, for the "frequently used" row.
CREATE TABLE link_clicks (
    user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    widget_id INTEGER NOT NULL REFERENCES widgets(id) ON DELETE CASCADE,
    count INTEGER NOT NULL DEFAULT 0,
    last_at TEXT NOT NULL,
    PRIMARY KEY (user_id, widget_id)
);

-- Background status checks of link tiles, one row per tile and day.
CREATE TABLE link_status (
    widget_id INTEGER NOT NULL REFERENCES widgets(id) ON DELETE CASCADE,
    day TEXT NOT NULL,
    ok INTEGER NOT NULL DEFAULT 0,
    fail INTEGER NOT NULL DEFAULT 0,
    ms_sum INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (widget_id, day)
);

-- Section placement on phones: '' normal, 'first', 'hide'.
ALTER TABLE sections ADD COLUMN mobile TEXT NOT NULL DEFAULT '';
