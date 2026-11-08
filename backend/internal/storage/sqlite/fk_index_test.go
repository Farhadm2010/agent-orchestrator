package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

// When a row is deleted, SQLite looks up the rows that reference it in every
// child table, whatever the foreign key's ON DELETE action. Without an index
// on the child column that lookup scans the whole child table once per
// deleted row, inside the transaction holding the daemon's only writer. A
// session delete paid that for every turn, review and the session itself:
// change_log alone was scanned twice (100,345 rows on a real archive). Every
// foreign key must have an index whose first column is its child column.
func TestEveryForeignKeyHasAnIndexOnItsChildColumns(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	db, err := sql.Open("sqlite", databaseURI(dir)+readOnlyPragmas)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	indexed := map[string]bool{}
	leading, err := db.Query(`
SELECT m.name, ii.name
FROM sqlite_master m
JOIN pragma_index_list(m.name) il
JOIN pragma_index_info(il.name) ii
WHERE m.type = 'table' AND ii.seqno = 0`)
	if err != nil {
		t.Fatalf("list indexes: %v", err)
	}
	for leading.Next() {
		var table string
		var column sql.NullString // NULL for an index on an expression
		if err := leading.Scan(&table, &column); err != nil {
			t.Fatalf("scan index: %v", err)
		}
		if column.Valid {
			indexed[table+"."+column.String] = true
		}
	}
	_ = leading.Close()
	// An INTEGER PRIMARY KEY is the rowid, which is its own index.
	rowids, err := db.Query(`
SELECT m.name, c.name
FROM sqlite_master m
JOIN pragma_table_info(m.name) c
WHERE m.type = 'table' AND c.pk = 1 AND upper(c.type) = 'INTEGER'
  AND (SELECT count(*) FROM pragma_table_info(m.name) WHERE pk > 0) = 1`)
	if err != nil {
		t.Fatalf("list rowid keys: %v", err)
	}
	for rowids.Next() {
		var table, column string
		if err := rowids.Scan(&table, &column); err != nil {
			t.Fatalf("scan rowid key: %v", err)
		}
		indexed[table+"."+column] = true
	}
	_ = rowids.Close()

	keys, err := db.Query(`
SELECT m.name, p."from", p."table"
FROM sqlite_master m
JOIN pragma_foreign_key_list(m.name) p
WHERE m.type = 'table'
ORDER BY m.name, p."from"`)
	if err != nil {
		t.Fatalf("list foreign keys: %v", err)
	}
	defer func() { _ = keys.Close() }()
	// Deliberately unindexed; see 20261109103000000_session_delete_fk_indexes.sql.
	unindexed := map[string]bool{
		"conversation_turns.handled_by_session_id": true,
	}
	var missing []string
	checked := 0
	for keys.Next() {
		var child, column, parent string
		if err := keys.Scan(&child, &column, &parent); err != nil {
			t.Fatalf("scan foreign key: %v", err)
		}
		checked++
		if !indexed[child+"."+column] && !unindexed[child+"."+column] {
			missing = append(missing, child+"."+column+" -> "+parent)
		}
	}
	if checked == 0 {
		t.Fatal("found no foreign keys; the schema query is wrong")
	}
	if len(missing) > 0 {
		t.Fatalf("foreign keys without an index on the child column (each delete of the parent scans the child table):\n  %s",
			strings.Join(missing, "\n  "))
	}
}
