package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func (s *Store) CreateAuthToken(ctx context.Context, userID string, deviceInfo *string, nowMs, expiresAtMs int64) (AuthTokenRow, error) {
	if s == nil || s.db == nil {
		return AuthTokenRow{}, fmt.Errorf("db not initialized")
	}

	token, err := generateToken()
	if err != nil {
		return AuthTokenRow{}, err
	}

	row := AuthTokenRow{
		Token:       token,
		UserID:      userID,
		DeviceInfo:  deviceInfo,
		CreatedAtMs: nowMs,
		ExpiresAtMs: expiresAtMs,
	}

	q := `INSERT INTO auth_tokens (token, user_id, device_info, created_at_ms, expires_at_ms)
		VALUES (?, ?, ?, ?, ?);`

	var deviceVal any
	if deviceInfo != nil {
		deviceVal = *deviceInfo
	}

	if _, err := s.db.ExecContext(ctx, s.rebind(q),
		row.Token, row.UserID, deviceVal, row.CreatedAtMs, row.ExpiresAtMs,
	); err != nil {
		return AuthTokenRow{}, err
	}

	return row, nil
}

func (s *Store) ValidateToken(ctx context.Context, token string, nowMs int64) (AuthTokenRow, error) {
	if s == nil || s.db == nil {
		return AuthTokenRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT token, user_id, device_info, created_at_ms, expires_at_ms
		FROM auth_tokens WHERE token = ?;`

	var row AuthTokenRow
	var device sql.NullString
	if err := s.db.QueryRowContext(ctx, s.rebind(q), token).Scan(
		&row.Token, &row.UserID, &device, &row.CreatedAtMs, &row.ExpiresAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return AuthTokenRow{}, ErrTokenInvalid
		}
		return AuthTokenRow{}, err
	}
	if device.Valid {
		row.DeviceInfo = &device.String
	}

	if nowMs > row.ExpiresAtMs {
		return AuthTokenRow{}, ErrTokenExpired
	}

	return row, nil
}

func (s *Store) DeleteToken(ctx context.Context, token string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}

	q := `DELETE FROM auth_tokens WHERE token = ?;`
	_, err := s.db.ExecContext(ctx, s.rebind(q), token)
	return err
}

func (s *Store) DeleteUserTokens(ctx context.Context, userID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}

	q := `DELETE FROM auth_tokens WHERE user_id = ?;`
	_, err := s.db.ExecContext(ctx, s.rebind(q), userID)
	return err
}

func (s *Store) CleanExpiredTokens(ctx context.Context, nowMs int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("db not initialized")
	}

	q := `DELETE FROM auth_tokens WHERE expires_at_ms < ?;`
	result, err := s.db.ExecContext(ctx, s.rebind(q), nowMs)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
