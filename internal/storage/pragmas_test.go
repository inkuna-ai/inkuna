package storage

import (
	"context"
	"path/filepath"
	"testing"
)

// Every connection the pool opens gets the settings, not only the first.
func TestEveryConnectionWaitsForLocksAndSyncsAtCheckpoints(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	for range 3 {
		connection, err := store.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = connection.Close() }()
		var journal string
		var synchronous, timeout int
		if err := connection.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, "PRAGMA synchronous").Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if err := connection.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatal(err)
		}
		// synchronous 1 is NORMAL.
		if journal != "wal" || synchronous != 1 || timeout != 5000 {
			t.Fatalf("journal %s, synchronous %d, busy timeout %d; want wal, 1, 5000", journal, synchronous, timeout)
		}
	}
}
