package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func applyMigrations(ctx context.Context, db *sql.DB, driver string) error {
	if err := ensureColumn(ctx, db, driver, "sessions", "source", "TEXT NOT NULL DEFAULT 'wechat_code'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "sessions", "kind", "TEXT NOT NULL DEFAULT 'direct'"); err != nil {
		return err
	}

	if err := ensureColumn(ctx, db, driver, "session_requests", "source", "TEXT NOT NULL DEFAULT 'wechat_code'"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "session_requests", "verification_message", "TEXT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "session_requests", "last_opened_at_ms", "BIGINT NOT NULL DEFAULT 0"); err != nil {
		return err
	}

	if err := ensureColumn(ctx, db, driver, "session_invites", "expires_at_ms", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "session_invites", "geo_fence_lat_e7", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "session_invites", "geo_fence_lng_e7", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "session_invites", "geo_fence_radius_m", "INTEGER"); err != nil {
		return err
	}

	if err := ensureColumn(ctx, db, driver, "activity_invites", "expires_at_ms", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "activity_invites", "geo_fence_lat_e7", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "activity_invites", "geo_fence_lng_e7", "BIGINT"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "activity_invites", "geo_fence_radius_m", "INTEGER"); err != nil {
		return err
	}

	if err := ensureColumn(ctx, db, driver, "home_bases", "daily_update_count", "INTEGER NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	if err := ensureColumn(ctx, db, driver, "home_bases", "visibility_radius_m", "INTEGER NOT NULL DEFAULT 1100"); err != nil {
		return err
	}

	stmts := []string{
		`CREATE INDEX IF NOT EXISTS idx_sessions_source_updated_at_ms ON sessions(source, updated_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_session_requests_requester_created_at_ms ON session_requests(requester_id, created_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_session_requests_requester_source_last_opened_at_ms ON session_requests(requester_id, source, last_opened_at_ms);`,
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func ensureColumn(ctx context.Context, db *sql.DB, driver, table, column, definition string) error {
	if !isSafeIdentifier(table) || !isSafeIdentifier(column) {
		return fmt.Errorf("unsafe identifier: table=%q column=%q", table, column)
	}

	exists, err := columnExists(ctx, db, driver, table, column)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s;", table, column, definition)
	if driver == "pgx" {
		stmt = fmt.Sprintf("ALTER TABLE %s ADD COLUMN IF NOT EXISTS %s %s;", table, column, definition)
	}

	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return err
	}
	return nil
}

func columnExists(ctx context.Context, db *sql.DB, driver, table, column string) (bool, error) {
	switch driver {
	case "sqlite":
		return columnExistsSQLite(ctx, db, table, column)
	case "pgx":
		return columnExistsPostgres(ctx, db, table, column)
	default:
		// Default to Postgres-compatible behavior for unknown drivers.
		return columnExistsPostgres(ctx, db, table, column)
	}
}

func columnExistsSQLite(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	q := fmt.Sprintf("PRAGMA table_info(%s);", table)
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var (
			cid     int
			name    string
			typ     string
			notnull int
			dflt    sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

func columnExistsPostgres(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	const q = `SELECT 1
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		AND table_name = $1
		AND column_name = $2;`
	var one int
	if err := db.QueryRowContext(ctx, q, table, column).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func isSafeIdentifier(s string) bool {
	if strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_':
		default:
			return false
		}
	}
	return true
}
