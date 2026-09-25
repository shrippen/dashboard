-- "Was tun?" answers per hint and language; digest detects changed hint text.
CREATE TABLE hint_advice (
    hint_id INTEGER NOT NULL REFERENCES hints(id) ON DELETE CASCADE,
    locale TEXT NOT NULL,
    digest TEXT NOT NULL,
    text TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (hint_id, locale)
);

-- Invoice fields read from a mail's attachments on request.
CREATE TABLE mail_reads (
    connection_id INTEGER NOT NULL REFERENCES connections(id) ON DELETE CASCADE,
    uid INTEGER NOT NULL,
    data TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (connection_id, uid)
);
