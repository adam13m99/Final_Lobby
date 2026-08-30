package store_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lobbybaz/coordinator/internal/store"
)

func TestBackupWritesAFileThatOpens(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "backups")
	at := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	name, err := store.Backup(db, out, at)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(name); err != nil || fi.Size() == 0 {
		t.Fatalf("backup file %s: %v", name, err)
	}

	// The point of the copy is that it can be opened and read, not that a
	// file of some size exists.
	copyDB, err := store.Open(name)
	if err != nil {
		t.Fatalf("the backup does not open: %v", err)
	}
	defer copyDB.Close()
	version, err := store.Version(copyDB)
	if err != nil {
		t.Fatalf("the backup has no schema: %v", err)
	}
	if want, _ := store.Version(db); version != want {
		t.Fatalf("backup schema %d, live schema %d", version, want)
	}
}

func TestBackupKeepsOnlyTheRecentCopies(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := store.Migrate(db); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "backups")
	at := time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC)
	for i := 0; i < store.BackupKeep+5; i++ {
		if _, err := store.Backup(db, out, at.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := os.ReadDir(out)
	if err != nil {
		t.Fatal(err)
	}
	kept := 0
	oldest := ""
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "lobby-") {
			kept++
			if oldest == "" || e.Name() < oldest {
				oldest = e.Name()
			}
		}
	}
	if kept != store.BackupKeep {
		t.Fatalf("kept %d copies, want %d", kept, store.BackupKeep)
	}
	// The five that went must be the five oldest, not five arbitrary ones.
	if want := "lobby-20260830-0500.db"; oldest != want {
		t.Fatalf("oldest kept copy is %s, want %s", oldest, want)
	}
}
