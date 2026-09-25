-- Daily value per key figure and space (owner 0 = shared data), for
-- trends and forecasts: "truenas.pool.tank.used" = 0.81 on 2026-09-25.
CREATE TABLE samples (
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner INTEGER NOT NULL DEFAULT 0,
    key TEXT NOT NULL,
    day TEXT NOT NULL,
    value REAL NOT NULL,
    PRIMARY KEY (space_id, owner, key, day)
);

-- Last seen version per subject; a change becomes an "update" event.
CREATE TABLE versions (
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner INTEGER NOT NULL DEFAULT 0,
    subject TEXT NOT NULL,
    version TEXT NOT NULL,
    seen_at TEXT NOT NULL,
    PRIMARY KEY (space_id, owner, subject)
);

-- Timeline: updates and other changes the analysis noticed.
CREATE TABLE events (
    id INTEGER PRIMARY KEY,
    space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    owner INTEGER NOT NULL DEFAULT 0,
    at TEXT NOT NULL,
    kind TEXT NOT NULL,
    subject TEXT NOT NULL,
    detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX events_space_at ON events (space_id, at);
