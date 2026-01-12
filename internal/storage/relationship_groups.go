package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) GetRelationshipGroupByID(ctx context.Context, userID, groupID string) (RelationshipGroupRow, error) {
	if s == nil || s.db == nil {
		return RelationshipGroupRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" || groupID == "" {
		return RelationshipGroupRow{}, fmt.Errorf("missing ids")
	}

	q := `SELECT id, user_id, name, created_at_ms, updated_at_ms
		FROM relationship_groups WHERE id = ? AND user_id = ?;`

	var g RelationshipGroupRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), groupID, userID).Scan(
		&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return RelationshipGroupRow{}, fmt.Errorf("%w: relationship group", ErrNotFound)
		}
		return RelationshipGroupRow{}, err
	}
	return g, nil
}

func (s *Store) ListRelationshipGroups(ctx context.Context, userID string) ([]RelationshipGroupRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return nil, fmt.Errorf("missing userID")
	}

	q := `SELECT id, user_id, name, created_at_ms, updated_at_ms
		FROM relationship_groups WHERE user_id = ?
		ORDER BY updated_at_ms DESC LIMIT 200;`
	rows, err := s.db.QueryContext(ctx, s.rebind(q), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RelationshipGroupRow
	for rows.Next() {
		var g RelationshipGroupRow
		if err := rows.Scan(&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) CreateRelationshipGroup(ctx context.Context, userID, name string, nowMs int64) (RelationshipGroupRow, bool, error) {
	if s == nil || s.db == nil {
		return RelationshipGroupRow{}, false, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return RelationshipGroupRow{}, false, fmt.Errorf("missing userID")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return RelationshipGroupRow{}, false, fmt.Errorf("missing name")
	}

	// Avoid pathological names.
	if len(name) > 30 {
		return RelationshipGroupRow{}, false, fmt.Errorf("name too long")
	}

	group := RelationshipGroupRow{
		ID:          uuid.NewString(),
		UserID:      userID,
		Name:        name,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	insertQ := `INSERT INTO relationship_groups (id, user_id, name, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, s.rebind(insertQ), group.ID, group.UserID, group.Name, group.CreatedAtMs, group.UpdatedAtMs); err != nil {
		if isUniqueViolation(err) {
			existing, err := s.getRelationshipGroupByName(ctx, userID, name)
			if err != nil {
				return RelationshipGroupRow{}, false, err
			}
			return existing, false, nil
		}
		return RelationshipGroupRow{}, false, err
	}
	return group, true, nil
}

func (s *Store) RenameRelationshipGroup(ctx context.Context, userID, groupID, name string, nowMs int64) (RelationshipGroupRow, error) {
	if s == nil || s.db == nil {
		return RelationshipGroupRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" || groupID == "" {
		return RelationshipGroupRow{}, fmt.Errorf("missing ids")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return RelationshipGroupRow{}, fmt.Errorf("missing name")
	}
	if len(name) > 30 {
		return RelationshipGroupRow{}, fmt.Errorf("name too long")
	}

	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return RelationshipGroupRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	current, err := getRelationshipGroupByIDInTx(txCtx, tx, s.driver, userID, groupID)
	if err != nil {
		return RelationshipGroupRow{}, err
	}
	if current.Name == name {
		return current, nil
	}

	updateQ := `UPDATE relationship_groups SET name = ?, updated_at_ms = ? WHERE id = ? AND user_id = ?;`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, updateQ), name, nowMs, groupID, userID); err != nil {
		if isUniqueViolation(err) {
			return RelationshipGroupRow{}, ErrGroupExists
		}
		return RelationshipGroupRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return RelationshipGroupRow{}, err
	}
	return s.GetRelationshipGroupByID(ctx, userID, groupID)
}

func (s *Store) DeleteRelationshipGroup(ctx context.Context, userID, groupID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	if userID == "" || groupID == "" {
		return fmt.Errorf("missing ids")
	}

	q := `DELETE FROM relationship_groups WHERE id = ? AND user_id = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), groupID, userID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: relationship group", ErrNotFound)
	}
	return nil
}

func (s *Store) getRelationshipGroupByName(ctx context.Context, userID, name string) (RelationshipGroupRow, error) {
	q := `SELECT id, user_id, name, created_at_ms, updated_at_ms
		FROM relationship_groups WHERE user_id = ? AND name = ?;`
	var g RelationshipGroupRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID, name).Scan(
		&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return RelationshipGroupRow{}, fmt.Errorf("%w: relationship group", ErrNotFound)
		}
		return RelationshipGroupRow{}, err
	}
	return g, nil
}

func getRelationshipGroupByIDInTx(ctx context.Context, tx *sql.Tx, driver, userID, groupID string) (RelationshipGroupRow, error) {
	if userID == "" || groupID == "" {
		return RelationshipGroupRow{}, fmt.Errorf("missing ids")
	}

	q := rebindQuery(driver, `SELECT id, user_id, name, created_at_ms, updated_at_ms
		FROM relationship_groups WHERE id = ? AND user_id = ?;`)
	var g RelationshipGroupRow
	if err := tx.QueryRowContext(ctx, q, groupID, userID).Scan(&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return RelationshipGroupRow{}, fmt.Errorf("%w: relationship group", ErrNotFound)
		}
		return RelationshipGroupRow{}, err
	}
	return g, nil
}

func getOrCreateRelationshipGroupByNameInTx(ctx context.Context, tx *sql.Tx, driver, userID, name string, nowMs int64) (RelationshipGroupRow, error) {
	userID = strings.TrimSpace(userID)
	name = strings.TrimSpace(name)
	if userID == "" || name == "" {
		return RelationshipGroupRow{}, fmt.Errorf("missing required fields")
	}
	if len(name) > 30 {
		return RelationshipGroupRow{}, fmt.Errorf("name too long")
	}

	// Fast path: select.
	selectQ := rebindQuery(driver, `SELECT id, user_id, name, created_at_ms, updated_at_ms
		FROM relationship_groups WHERE user_id = ? AND name = ?;`)
	var g RelationshipGroupRow
	if err := tx.QueryRowContext(ctx, selectQ, userID, name).Scan(&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs); err == nil {
		return g, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return RelationshipGroupRow{}, err
	}

	g = RelationshipGroupRow{
		ID:          uuid.NewString(),
		UserID:      userID,
		Name:        name,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}
	insertQ := rebindQuery(driver, `INSERT INTO relationship_groups (id, user_id, name, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?);`)
	if _, err := tx.ExecContext(ctx, insertQ, g.ID, g.UserID, g.Name, g.CreatedAtMs, g.UpdatedAtMs); err != nil {
		if isUniqueViolation(err) {
			// Race: select again.
			if err := tx.QueryRowContext(ctx, selectQ, userID, name).Scan(&g.ID, &g.UserID, &g.Name, &g.CreatedAtMs, &g.UpdatedAtMs); err != nil {
				return RelationshipGroupRow{}, err
			}
			return g, nil
		}
		return RelationshipGroupRow{}, err
	}
	return g, nil
}
