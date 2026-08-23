package store_test

import (
	"testing"

	"lobbybaz/coordinator/internal/store"
)

func TestOpenAppliesEveryMigration(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	v, err := store.Version(db)
	if err != nil {
		t.Fatal(err)
	}
	if v < 3 {
		t.Fatalf("schema version = %d, want at least 3", v)
	}

	for _, table := range []string{
		"accounts", "contact_methods", "sessions", "terms_acceptance",
		"kick_events", "role_grants", "admin_actions",
	} {
		var name string
		err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}
}

// Running migrations twice must be a no-op, because the coordinator applies
// them on every start.
func TestMigrateIsIdempotent(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	before, _ := store.Version(db)
	if err := store.Migrate(db); err != nil {
		t.Fatalf("second run failed: %v", err)
	}
	after, _ := store.Version(db)
	if before != after {
		t.Fatalf("version moved from %d to %d on a no-op run", before, after)
	}
}

// The contact table is the seam for email and SMS (D37). It must exist and be
// empty - shipping it populated, or not shipping it, both defeat the point.
func TestContactMethodsShipsEmpty(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM contact_methods`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("contact_methods has %d rows, want 0", n)
	}
}

// The migrations are heavily commented on purpose, and a semicolon inside a
// comment used to end the statement early - a failure that only appears when
// somebody writes a good comment.
func TestSemicolonsInsideCommentsAndLiteralsDoNotSplitStatements(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// The real schema exercises both: migration 5's sanctions comment contains
	// a semicolon, and several CHECK constraints contain quoted literals.
	for _, table := range []string{"sanctions", "player_labels", "banners"} {
		var name string
		if err := db.QueryRow(
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, table,
		).Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
		}
	}

	// And the constraint written with quoted literals is really in force.
	_, err = db.Exec(
		`INSERT INTO sanctions (id, account_id, kind, reason, actor_id, created_at)
		 VALUES ('s1', 'a1', 'not-a-kind', '', 'a2', '2026-08-24T00:00:00Z')`)
	if err == nil {
		t.Error("a nonsense sanction kind was accepted; the CHECK constraint was lost in splitting")
	}
}
