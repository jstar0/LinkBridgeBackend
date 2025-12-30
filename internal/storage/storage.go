package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Store struct {
	db     *sql.DB
	driver string
	logger *slog.Logger
}

func Open(ctx context.Context, databaseURL string, logger *slog.Logger) (*Store, error) {
	if strings.TrimSpace(databaseURL) == "" {
		return nil, fmt.Errorf("DATABASE_URL is required")
	}
	u, err := url.Parse(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse DATABASE_URL: %w", err)
	}

	driverName, dsn, err := driverAndDSN(u, databaseURL)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}

	store := &Store{
		db:     db,
		driver: driverName,
		logger: logger,
	}

	switch driverName {
	case "sqlite":
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := store.applyConnectionTuning(pingCtx, driverName); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := store.Ready(pingCtx); err != nil {
		_ = db.Close()
		return nil, err
	}

	if err := initSchema(pingCtx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Ready(ctx context.Context) error {
	if s == nil || s.db == nil {
		return errors.New("db not initialized")
	}
	if err := s.db.PingContext(ctx); err != nil {
		return err
	}
	var one int
	if err := s.db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	if one != 1 {
		return fmt.Errorf("unexpected SELECT 1 result: %d", one)
	}
	return nil
}

func (s *Store) applyConnectionTuning(ctx context.Context, driver string) error {
	switch driver {
	case "sqlite":
		// SQLite foreign keys are per-connection, so with max_open_conns=1 this is sufficient.
		conn, err := s.db.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON;"); err != nil {
			return err
		}
		return nil
	default:
		return nil
	}
}

func driverAndDSN(u *url.URL, raw string) (driver string, dsn string, _ error) {
	switch strings.ToLower(u.Scheme) {
	case "sqlite":
		dsn, err := sqliteDSN(u, raw)
		if err != nil {
			return "", "", err
		}
		return "sqlite", dsn, nil
	case "postgres", "postgresql":
		return "pgx", raw, nil
	default:
		return "", "", fmt.Errorf("unsupported DATABASE_URL scheme %q (expected sqlite:// or postgres://)", u.Scheme)
	}
}

func sqliteDSN(u *url.URL, raw string) (string, error) {
	// Supported:
	// - sqlite:///absolute/path.db
	// - sqlite:relative/path.db
	// - sqlite::memory:
	switch {
	case u.Opaque != "":
		return u.Opaque, nil
	case u.Path != "":
		return u.Path, nil
	default:
		return "", fmt.Errorf("invalid sqlite DATABASE_URL %q", raw)
	}
}

func RedactedDatabaseURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<invalid>"
	}

	switch strings.ToLower(u.Scheme) {
	case "sqlite":
		// For sqlite, path is not sensitive.
		if u.Opaque != "" {
			return "sqlite:" + u.Opaque
		}
		return "sqlite://" + u.Path
	case "postgres", "postgresql":
		redacted := *u
		if redacted.User != nil {
			user := redacted.User.Username()
			redacted.User = url.UserPassword(user, "***")
		}
		return redacted.String()
	default:
		return "<unknown>"
	}
}
