package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func (s *Store) GetOrCreateSessionInvite(ctx context.Context, inviterID string, nowMs int64) (SessionInviteRow, bool, error) {
	if s == nil || s.db == nil {
		return SessionInviteRow{}, false, fmt.Errorf("db not initialized")
	}
	if inviterID == "" {
		return SessionInviteRow{}, false, fmt.Errorf("missing inviterID")
	}

	// Reuse an existing code to keep the QR stable.
	const selectQ = `SELECT code, inviter_id, created_at_ms, updated_at_ms FROM session_invites WHERE inviter_id = ?;`
	var existing SessionInviteRow
	if err := s.db.QueryRowContext(ctx, s.rebind(selectQ), inviterID).Scan(
		&existing.Code, &existing.InviterID, &existing.CreatedAtMs, &existing.UpdatedAtMs,
	); err == nil {
		return existing, false, nil
	} else if err != sql.ErrNoRows {
		return SessionInviteRow{}, false, err
	}

	for i := 0; i < 3; i++ {
		code, err := newInviteCode(8) // 16 hex chars
		if err != nil {
			return SessionInviteRow{}, false, err
		}
		row := SessionInviteRow{
			Code:        code,
			InviterID:   inviterID,
			CreatedAtMs: nowMs,
			UpdatedAtMs: nowMs,
		}

		const insertQ = `INSERT INTO session_invites (code, inviter_id, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?);`
		if _, err := s.db.ExecContext(ctx, s.rebind(insertQ), row.Code, row.InviterID, row.CreatedAtMs, row.UpdatedAtMs); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return SessionInviteRow{}, false, err
		}
		return row, true, nil
	}

	return SessionInviteRow{}, false, fmt.Errorf("failed to create invite code")
}

func (s *Store) ResolveSessionInvite(ctx context.Context, code string) (SessionInviteRow, error) {
	if s == nil || s.db == nil {
		return SessionInviteRow{}, fmt.Errorf("db not initialized")
	}
	if code == "" {
		return SessionInviteRow{}, fmt.Errorf("missing code")
	}

	const q = `SELECT code, inviter_id, created_at_ms, updated_at_ms FROM session_invites WHERE code = ?;`
	var row SessionInviteRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), code).Scan(&row.Code, &row.InviterID, &row.CreatedAtMs, &row.UpdatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return SessionInviteRow{}, ErrInviteInvalid
		}
		return SessionInviteRow{}, err
	}
	return row, nil
}

func newInviteCode(nBytes int) (string, error) {
	if nBytes <= 0 {
		nBytes = 8
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
