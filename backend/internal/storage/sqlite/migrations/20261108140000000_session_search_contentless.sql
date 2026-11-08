-- +goose Up
-- Session search stores each document's text once, compressed, beside a
-- contentless full-text index.
--
-- The first schema kept the text in plain columns that the FTS5 table
-- referenced as its external content, so every word was on disk twice: once
-- as text and once in the index. On a real archive the text was three times
-- the size of the index (121 MB against 41 MB), almost all of it command
-- output. FTS5 only needs the text to compute snippets, and snippets are cut
-- in Go from the page's rows, so the text now lives in a compressed column
-- and the index is contentless.
--
-- Writes change too. The old triggers inserted into the index one document at
-- a time, and FTS5 flushes its pending terms at every statement savepoint, so
-- each document became its own index segment that later had to be merged. The
-- store now writes a batch's index rows in one statement.
--
-- The index is derived data. Clearing it and its source cursors makes the
-- indexer rebuild it from the transcripts on its next pass.

-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_docs_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_text_au;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_text_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_text_ai;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_fts;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_text;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM session_search_docs;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM session_search_sources;
-- +goose StatementEnd

-- +goose StatementBegin
-- body is the document's text, compressed (see store.encodeSearchBody); hash
-- fingerprints title, body and paths so a pass that re-reads an unchanged
-- document skips rewriting it in the index.
CREATE TABLE session_search_text (
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT '',
    body   BLOB,
    hash   INTEGER NOT NULL DEFAULT 0
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Contentless: the index keeps postings only. A row is removed with FTS5's
-- 'delete' command and the exact text it was indexed with, read back from
-- session_search_text. (contentless_delete would remove a row by rowid alone,
-- but its tombstones keep counting in term statistics until segments merge,
-- which moved bm25 scores after every edit or prune.)
CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='',
    tokenize='unicode61 remove_diacritics 2'
);
-- +goose StatementEnd

-- +goose StatementBegin
-- Deleting a document removes its index row and its text, so a pruned or
-- cleared source can never leave a searchable orphan. ao_search_body() is
-- registered by the daemon (storage/sqlite/search_functions.go) and decodes
-- the stored text the index row was built from.
CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    SELECT 'delete', t.id, t.title, ao_search_body(t.body), t.paths
    FROM session_search_text t WHERE t.id = old.id;
    DELETE FROM session_search_text WHERE id = old.id;
END;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TRIGGER IF EXISTS session_search_docs_ad;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_fts;
-- +goose StatementEnd
-- +goose StatementBegin
DROP TABLE IF EXISTS session_search_text;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM session_search_docs;
-- +goose StatementEnd
-- +goose StatementBegin
DELETE FROM session_search_sources;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TABLE session_search_text (
    id     INTEGER PRIMARY KEY,
    title  TEXT NOT NULL DEFAULT '',
    body   TEXT NOT NULL DEFAULT '',
    paths  TEXT NOT NULL DEFAULT ''
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE VIRTUAL TABLE session_search_fts USING fts5(
    title,
    body,
    paths,
    content='session_search_text',
    content_rowid='id',
    tokenize='unicode61 remove_diacritics 2'
);
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_search_text_ai AFTER INSERT ON session_search_text BEGIN
    INSERT INTO session_search_fts (rowid, title, body, paths)
    VALUES (new.id, new.title, new.body, new.paths);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_search_text_ad AFTER DELETE ON session_search_text BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    VALUES ('delete', old.id, old.title, old.body, old.paths);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_search_text_au AFTER UPDATE ON session_search_text BEGIN
    INSERT INTO session_search_fts (session_search_fts, rowid, title, body, paths)
    VALUES ('delete', old.id, old.title, old.body, old.paths);
    INSERT INTO session_search_fts (rowid, title, body, paths)
    VALUES (new.id, new.title, new.body, new.paths);
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER session_search_docs_ad AFTER DELETE ON session_search_docs BEGIN
    DELETE FROM session_search_text WHERE id = old.id;
END;
-- +goose StatementEnd
