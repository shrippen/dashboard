-- Events pushed by services without a read API (PG Back Web webhooks).
CREATE TABLE hook_events (
    id INTEGER PRIMARY KEY,
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    event TEXT NOT NULL,
    subject TEXT NOT NULL,
    at TEXT NOT NULL
);
CREATE INDEX hook_events_conn ON hook_events(connection_id, at);
