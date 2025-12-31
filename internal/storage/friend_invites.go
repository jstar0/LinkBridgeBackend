package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
)

func (s *Store) GetOrCreateFriendInvite(ctx context.Context, inviterID string, nowMs int64) (FriendInviteRow, bool, error) {
	if s == nil || s.db == nil {
		return FriendInviteRow{}, false, fmt.Errorf("db not initialized")
	}
	if inviterID == "" {
		return FriendInviteRow{}, false, fmt.Errorf("missing inviterID")
	}

	// Reuse an existing code to keep the QR stable.
	const selectQ = `SELECT code, inviter_id, created_at_ms, updated_at_ms FROM friend_invites WHERE inviter_id = ?;`
	var existing FriendInviteRow
	if err := s.db.QueryRowContext(ctx, s.rebind(selectQ), inviterID).Scan(
		&existing.Code, &existing.InviterID, &existing.CreatedAtMs, &existing.UpdatedAtMs,
	); err == nil {
		return existing, false, nil
	} else if err != sql.ErrNoRows {
		return FriendInviteRow{}, false, err
	}

	for i := 0; i < 3; i++ {
		code, err := newInviteCode(8) // 16 hex chars
		if err != nil {
			return FriendInviteRow{}, false, err
		}
		row := FriendInviteRow{
			Code:        code,
			InviterID:   inviterID,
			CreatedAtMs: nowMs,
			UpdatedAtMs: nowMs,
		}

		const insertQ = `INSERT INTO friend_invites (code, inviter_id, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?);`
		if _, err := s.db.ExecContext(ctx, s.rebind(insertQ), row.Code, row.InviterID, row.CreatedAtMs, row.UpdatedAtMs); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return FriendInviteRow{}, false, err
		}
		return row, true, nil
	}

	return FriendInviteRow{}, false, fmt.Errorf("failed to create invite code")
}

func (s *Store) ResolveFriendInvite(ctx context.Context, code string) (FriendInviteRow, error) {
	if s == nil || s.db == nil {
		return FriendInviteRow{}, fmt.Errorf("db not initialized")
	}
	if code == "" {
		return FriendInviteRow{}, fmt.Errorf("missing code")
	}

	const q = `SELECT code, inviter_id, created_at_ms, updated_at_ms FROM friend_invites WHERE code = ?;`
	var row FriendInviteRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), code).Scan(&row.Code, &row.InviterID, &row.CreatedAtMs, &row.UpdatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return FriendInviteRow{}, ErrInviteInvalid
		}
		return FriendInviteRow{}, err
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
