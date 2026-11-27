package storage

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestADatabaseANewerBuildMigratedIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.db")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// What a newer build's migration leaves.
	if _, err := store.db.Exec(`INSERT INTO schema_migrations(version, applied_at) VALUES ('999_from_a_newer_build.sql', ?)`, timestamp(time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(path)
	var newer NewerDatabaseError
	if !errors.As(err, &newer) || !slices.Equal(newer.Unknown, []string{"999_from_a_newer_build.sql"}) {
		t.Fatalf("a database with a migration this build does not know must be refused: %v", err)
	}
	applied, err := AppliedMigrations(path)
	if err != nil || len(applied) != len(Migrations())+1 || applied[len(applied)-1] != "999_from_a_newer_build.sql" {
		t.Fatalf("applied migrations %v, err %v", applied, err)
	}
}

func TestAppliedMigrationsReadsWithoutCreatingOrMigrating(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inkuna.db")
	if applied, err := AppliedMigrations(path); err != nil || applied != nil {
		t.Fatalf("a database that does not exist has none: %v %v", applied, err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if applied, err := AppliedMigrations(path); err != nil || !slices.Equal(applied, Migrations()) {
		t.Fatalf("applied %v, err %v", applied, err)
	}
	if LatestMigration() != Migrations()[len(Migrations())-1] {
		t.Fatalf("latest %s", LatestMigration())
	}
}

// Another process, as inkunactl beside the app, can apply a migration
// between Migrate's look and its transaction; the transaction checks again
// under the write lock and leaves it be.
func TestAMigrationAppliedMeanwhileIsNotAppliedAgain(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "inkuna.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	// Run twice, this would fail on the table that already exists.
	contents := `CREATE TABLE applied_once (id INTEGER PRIMARY KEY)`
	if err := store.applyMigration(ctx, "998_applied_once.sql", contents); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigration(ctx, "998_applied_once.sql", contents); err != nil {
		t.Fatalf("a migration applied meanwhile must be skipped: %v", err)
	}
}
