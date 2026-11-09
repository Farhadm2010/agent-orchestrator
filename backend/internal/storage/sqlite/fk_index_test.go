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
	for _, pair := range queryPairs(t, db, `
SELECT m.name, ii.name
FROM sqlite_master m
JOIN pragma_index_list(m.name) il
JOIN pragma_index_info(il.name) ii
WHERE m.type = 'table' AND ii.seqno = 0 AND ii.name IS NOT NULL`) {
		indexed[pair[0]+"."+pair[1]] = true
	}
	// An INTEGER PRIMARY KEY is the rowid, which is its own index.
	for _, pair := range queryPairs(t, db, `
SELECT m.name, c.name
FROM sqlite_master m
JOIN pragma_table_info(m.name) c
WHERE m.type = 'table' AND c.pk = 1 AND upper(c.type) = 'INTEGER'
  AND (SELECT count(*) FROM pragma_table_info(m.name) WHERE pk > 0) = 1`) {
		indexed[pair[0]+"."+pair[1]] = true
	}
	// Deliberately unindexed; see 20261109103000000_session_delete_fk_indexes.sql.
	unindexed := map[string]bool{
		"conversation_turns.handled_by_session_id": true,
	}
	var missing []string
	checked := 0
	for _, pair := range queryPairs(t, db, `
SELECT m.name || '.' || p."from", p."table"
FROM sqlite_master m
JOIN pragma_foreign_key_list(m.name) p
WHERE m.type = 'table'
ORDER BY m.name, p."from"`) {
		checked++
		if !indexed[pair[0]] && !unindexed[pair[0]] {
			missing = append(missing, pair[0]+" -> "+pair[1])
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

// queryPairs runs a query of two text columns and returns its rows.
func queryPairs(t *testing.T, db *sql.DB, query string) [][2]string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var out [][2]string
	for rows.Next() {
		var pair [2]string
		if err := rows.Scan(&pair[0], &pair[1]); err != nil {
			t.Fatalf("scan: %v", err)
		}
		out = append(out, pair)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	return out
}
