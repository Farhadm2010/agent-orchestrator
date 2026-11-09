-- +goose Up
-- The session search index moves out of ao.db into its own file, search.db
-- (storage/sqlite/search_db.go).
--
-- SQLite allows one writer per database file, and every write the daemon
-- makes (chat streaming, session state) goes through ao.db's. Index writes in
-- the same file made those wait: a rebuild held the lock for up to a second at
-- a time, and FTS5 merges and checkpoints inside small batches still took 30
-- to 140 ms. In its own file the index holds only its own lock.
--
-- The index is derived data: the indexer rebuilds it in search.db from the
-- sessions on its next pass. What stays in ao.db is the one thing a session
-- delete must do in its own transaction: record the deleted id, a single row.
-- Search ignores that session's documents at once and the indexer removes them
-- from search.db afterwards. Session ids are never reused (session_num_floors),
-- so a recorded id stays deleted.

-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_search_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_docs_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_vocab;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_fts;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_text;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_docs;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_sources;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE session_search_deleted (
    session_id TEXT PRIMARY KEY
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_search_ad AFTER DELETE ON sessions BEGIN
    INSERT OR IGNORE INTO session_search_deleted (session_id) VALUES (old.id);
END;
-- +goose StatementEnd
-- +goose StatementBegin
-- The token search.db records as the database it indexes. A search.db beside
-- a different ao.db (a restored or swapped database) holds another token and
-- is cleared and rebuilt, instead of describing sessions that do not exist.
CREATE TABLE session_search_owner (
    id    INTEGER PRIMARY KEY CHECK (id = 1),
    token TEXT NOT NULL
);
-- +goose StatementEnd

-- +goose Down
-- Back to the in-file index, empty: the indexer rebuilds it.
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_owner;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS sessions_search_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_deleted;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE session_search_docs (
    id          INTEGER PRIMARY KEY,
    source_key  TEXT NOT NULL,
    doc_key     TEXT NOT NULL,
    session_id  TEXT NOT NULL DEFAULT '',
    project_id  TEXT NOT NULL DEFAULT '',
    harness     TEXT NOT NULL DEFAULT '',
    machine     TEXT NOT NULL DEFAULT '',
    kind        TEXT NOT NULL CHECK (kind IN (
        'title', 'user', 'assistant', 'tool', 'command', 'path'
    )),
    message_id  TEXT NOT NULL DEFAULT '',
    seq         INTEGER NOT NULL DEFAULT 0,
    created_at  INTEGER NOT NULL,
    indexed_at  INTEGER NOT NULL,
    UNIQUE (source_key, doc_key, kind)
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_search_docs_session ON session_search_docs (session_id);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_search_docs_project ON session_search_docs (project_id, created_at DESC);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_search_docs_source ON session_search_docs (source_key);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE session_search_text (
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT '',
    body   BLOB,
    hash   INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='',
    tokenize='unicode61 remove_diacritics 2'
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE VIRTUAL TABLE session_search_vocab USING fts5vocab(session_search_fts, 'row');
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    SELECT 'delete', t.id, t.title, ao_search_body(t.body), t.paths
    FROM session_search_text t WHERE t.id = old.id;
    DELETE FROM session_search_text WHERE id = old.id;
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE session_search_sources (
    source_key      TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('chat', 'native')),
    session_id      TEXT NOT NULL DEFAULT '',
    project_id      TEXT NOT NULL DEFAULT '',
    harness         TEXT NOT NULL DEFAULT '',
    path            TEXT NOT NULL DEFAULT '',
    cursor_value    INTEGER NOT NULL DEFAULT 0,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_mtime      INTEGER NOT NULL DEFAULT 0,
    doc_count       INTEGER NOT NULL DEFAULT 0,
    fingerprint     TEXT NOT NULL DEFAULT '',
    stamp           TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL DEFAULT 'pending'
        CHECK (state IN ('pending', 'active', 'unavailable', 'unsupported', 'no_transcript')),
    last_error_code TEXT NOT NULL DEFAULT '',
    updated_at      INTEGER NOT NULL
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE INDEX idx_session_search_sources_state ON session_search_sources (state, updated_at);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER sessions_search_ad AFTER DELETE ON sessions BEGIN
    DELETE FROM session_search_docs WHERE session_id = old.id;
    DELETE FROM session_search_sources WHERE session_id = old.id;
END;
-- +goose StatementEnd
