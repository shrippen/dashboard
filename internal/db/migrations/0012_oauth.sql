-- Sign-in instead of a token: the OAuth client a manager registered in the
-- service (Gitea, Snipe-IT, Tailscale), and the rotating tokens per
-- connection and user (user_id 0 = the shared credential).
ALTER TABLE connections ADD COLUMN oauth_client_enc BLOB;
CREATE TABLE oauth_grants (
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL DEFAULT 0,
    grant_enc BLOB NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (connection_id, user_id)
);
