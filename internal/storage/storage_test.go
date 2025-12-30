package storage

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"testing"
)

func TestDriverAndDSN_SQLitePath(t *testing.T) {
	u, err := url.Parse("sqlite:///tmp/linkbridge.db")
	if err != nil {
		t.Fatalf("url.Parse error = %v", err)
	}

	driver, dsn, err := driverAndDSN(u, "sqlite:///tmp/linkbridge.db")
	if err != nil {
		t.Fatalf("driverAndDSN error = %v", err)
	}
	if driver != "sqlite" {
		t.Fatalf("driver = %q, want %q", driver, "sqlite")
	}
	if dsn != "/tmp/linkbridge.db" {
		t.Fatalf("dsn = %q, want %q", dsn, "/tmp/linkbridge.db")
	}
}

func TestDriverAndDSN_SQLiteMemory(t *testing.T) {
	u, err := url.Parse("sqlite::memory:")
	if err != nil {
		t.Fatalf("url.Parse error = %v", err)
	}

	driver, dsn, err := driverAndDSN(u, "sqlite::memory:")
	if err != nil {
		t.Fatalf("driverAndDSN error = %v", err)
	}
	if driver != "sqlite" {
		t.Fatalf("driver = %q, want %q", driver, "sqlite")
	}
	if dsn != ":memory:" {
		t.Fatalf("dsn = %q, want %q", dsn, ":memory:")
	}
}

func TestRedactedDatabaseURL_PostgresRedactsPassword(t *testing.T) {
	got := RedactedDatabaseURL("postgres://alice:secret@localhost:5432/linkbridge")
	if got == "postgres://alice:secret@localhost:5432/linkbridge" {
		t.Fatalf("expected password to be redacted, got %q", got)
	}
}

func TestOpen_SQLiteInMemory_InitializesSchemaAndFK(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	if err := store.Ready(ctx); err != nil {
		t.Fatalf("Ready() error = %v", err)
	}

	// Verify schema exists.
	for _, table := range []string{"sessions", "messages"} {
		var name string
		if err := store.db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name=?;", table).Scan(&name); err != nil {
			t.Fatalf("expected table %q to exist: %v", table, err)
		}
	}

	// Verify foreign keys are enabled.
	var fk int
	if err := store.db.QueryRowContext(ctx, "PRAGMA foreign_keys;").Scan(&fk); err != nil {
		t.Fatalf("PRAGMA foreign_keys error = %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
}
