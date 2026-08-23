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
