package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

func (s *Store) CreateUser(ctx context.Context, username, passwordHash, displayName string, nowMs int64) (UserRow, error) {
	if s == nil || s.db == nil {
		return UserRow{}, fmt.Errorf("db not initialized")
	}

	userID := uuid.NewString()
	user := UserRow{
		ID:           userID,
		Username:     username,
		PasswordHash: passwordHash,
		DisplayName:  displayName,
		CreatedAtMs:  nowMs,
		UpdatedAtMs:  nowMs,
	}

	q := `INSERT INTO users (id, username, password_hash, display_name, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, s.rebind(q),
		user.ID, user.Username, user.PasswordHash, user.DisplayName, nowMs, nowMs,
	); err != nil {
		if isUniqueViolation(err) {
			return UserRow{}, ErrUsernameExists
		}
		return UserRow{}, err
	}

	return user, nil
}

func (s *Store) GetUserByID(ctx context.Context, userID string) (UserRow, error) {
	if s == nil || s.db == nil {
		return UserRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, username, password_hash, display_name, avatar_url, created_at_ms, updated_at_ms
		FROM users WHERE id = ?;`

	var user UserRow
	var avatar sql.NullString
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&avatar, &user.CreatedAtMs, &user.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return UserRow{}, fmt.Errorf("%w: user", ErrNotFound)
		}
		return UserRow{}, err
	}
	if avatar.Valid {
		user.AvatarURL = &avatar.String
	}

	return user, nil
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (UserRow, error) {
	if s == nil || s.db == nil {
		return UserRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, username, password_hash, display_name, avatar_url, created_at_ms, updated_at_ms
		FROM users WHERE username = ?;`

	var user UserRow
	var avatar sql.NullString
	if err := s.db.QueryRowContext(ctx, s.rebind(q), username).Scan(
		&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
		&avatar, &user.CreatedAtMs, &user.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return UserRow{}, fmt.Errorf("%w: user", ErrNotFound)
		}
		return UserRow{}, err
	}
	if avatar.Valid {
		user.AvatarURL = &avatar.String
	}

	return user, nil
}

func (s *Store) SearchUsers(ctx context.Context, query string, limit int) ([]UserRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 {
		limit = 20
	}

	q := `SELECT id, username, password_hash, display_name, avatar_url, created_at_ms, updated_at_ms
		FROM users WHERE username LIKE ? OR display_name LIKE ? LIMIT ?;`

	pattern := "%" + query + "%"
	rows, err := s.db.QueryContext(ctx, s.rebind(q), pattern, pattern, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []UserRow
	for rows.Next() {
		var user UserRow
		var avatar sql.NullString
		if err := rows.Scan(
			&user.ID, &user.Username, &user.PasswordHash, &user.DisplayName,
			&avatar, &user.CreatedAtMs, &user.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if avatar.Valid {
			user.AvatarURL = &avatar.String
		}
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (s *Store) UpdateUserDisplayName(ctx context.Context, userID, displayName string, nowMs int64) (UserRow, error) {
	if s == nil || s.db == nil {
		return UserRow{}, fmt.Errorf("db not initialized")
	}

	q := `UPDATE users SET display_name = ?, updated_at_ms = ? WHERE id = ?;`
	result, err := s.db.ExecContext(ctx, s.rebind(q), displayName, nowMs, userID)
	if err != nil {
		return UserRow{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return UserRow{}, fmt.Errorf("%w: user", ErrNotFound)
	}

	return s.GetUserByID(ctx, userID)
}

func isUniqueViolation(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "unique_violation")
}
