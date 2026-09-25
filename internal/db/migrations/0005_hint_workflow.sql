-- Hint history: every state change with who and an optional note.
CREATE TABLE hint_events (
    id INTEGER PRIMARY KEY,
    hint_id INTEGER NOT NULL REFERENCES hints(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    note TEXT NOT NULL DEFAULT '',
    at TEXT NOT NULL
);
CREATE INDEX hint_events_hint ON hint_events (hint_id, at);

-- Who works on a hint, and how far they got.
CREATE TABLE hint_work (
    hint_id INTEGER PRIMARY KEY REFERENCES hints(id) ON DELETE CASCADE,
    assignee_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
    state TEXT NOT NULL DEFAULT 'open',
    at TEXT NOT NULL
);

-- Channel subscriptions: only hints of these sources ('[]' = all).
ALTER TABLE notify_channels ADD COLUMN sources TEXT NOT NULL DEFAULT '[]';
