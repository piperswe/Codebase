-- 000002_mail: local mail index. The body column keeps the full extracted
-- text; embedding is reserved for the future vector search release.

CREATE TABLE mailboxes (
    name         TEXT PRIMARY KEY,
    uidvalidity  INTEGER NOT NULL DEFAULT 0,
    last_uid     INTEGER NOT NULL DEFAULT 0,
    synced_at    INTEGER
);

CREATE TABLE mail_messages (
    id           INTEGER PRIMARY KEY,
    mailbox      TEXT NOT NULL,
    uid          INTEGER NOT NULL,
    message_id   TEXT,
    subject      TEXT,
    sender       TEXT,
    recipients   TEXT,             -- JSON array
    date         INTEGER,          -- unix seconds
    snippet      TEXT,
    body         TEXT,
    flags        TEXT,             -- JSON array, e.g. ["\\Seen"]
    size         INTEGER,
    embedding    BLOB,             -- reserved: brute-force cosine search, later release
    UNIQUE (mailbox, uid)
);

CREATE INDEX mail_messages_date ON mail_messages (date DESC);
CREATE INDEX mail_messages_mailbox ON mail_messages (mailbox, uid);

CREATE VIRTUAL TABLE mail_fts USING fts5(subject, sender, recipients, body, content='mail_messages', content_rowid='id');

CREATE TRIGGER mail_ai AFTER INSERT ON mail_messages BEGIN
    INSERT INTO mail_fts(rowid, subject, sender, recipients, body)
    VALUES (new.id, new.subject, new.sender, new.recipients, new.body);
END;

CREATE TRIGGER mail_ad AFTER DELETE ON mail_messages BEGIN
    INSERT INTO mail_fts(mail_fts, rowid, subject, sender, recipients, body)
    VALUES ('delete', old.id, old.subject, old.sender, old.recipients, old.body);
END;

CREATE TRIGGER mail_au AFTER UPDATE ON mail_messages BEGIN
    INSERT INTO mail_fts(mail_fts, rowid, subject, sender, recipients, body)
    VALUES ('delete', old.id, old.subject, old.sender, old.recipients, old.body);
    INSERT INTO mail_fts(rowid, subject, sender, recipients, body)
    VALUES (new.id, new.subject, new.sender, new.recipients, new.body);
END;
