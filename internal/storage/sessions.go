package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"

	"github.com/google/uuid"
)

func computeParticipantsHash(user1ID, user2ID string) string {
	ids := []string{user1ID, user2ID}
	sort.Strings(ids)
	h := sha256.Sum256([]byte(ids[0] + ":" + ids[1]))
	return hex.EncodeToString(h[:])
}

func (s *Store) CreateSession(ctx context.Context, currentUserID, peerUserID string, nowMs int64) (SessionRow, bool, error) {
	if s == nil || s.db == nil {
		return SessionRow{}, false, fmt.Errorf("db not initialized")
	}
	if currentUserID == peerUserID {
		return SessionRow{}, false, ErrCannotChatSelf
	}

	hash := computeParticipantsHash(currentUserID, peerUserID)

	existing, err := s.getSessionByHash(ctx, hash)
	if err == nil {
		return existing, false, nil
	}

	ids := []string{currentUserID, peerUserID}
	sort.Strings(ids)

	sessionID := uuid.NewString()
	session := SessionRow{
		ID:               sessionID,
		ParticipantsHash: hash,
		User1ID:          ids[0],
		User2ID:          ids[1],
		Status:           SessionStatusActive,
		CreatedAtMs:      nowMs,
		UpdatedAtMs:      nowMs,
	}

	q := `INSERT INTO sessions (id, participants_hash, user1_id, user2_id, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, s.rebind(q),
		session.ID, session.ParticipantsHash, session.User1ID, session.User2ID,
		session.Status, nowMs, nowMs,
	); err != nil {
		if isUniqueViolation(err) {
			existing, err := s.getSessionByHash(ctx, hash)
			if err != nil {
				return SessionRow{}, false, err
			}
			return existing, false, nil
		}
		return SessionRow{}, false, err
	}

	return session, true, nil
}

func (s *Store) getSessionByHash(ctx context.Context, hash string) (SessionRow, error) {
	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms
		FROM sessions WHERE participants_hash = ?;`

	var session SessionRow
	var lastText sql.NullString
	var lastAtMs sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(q), hash).Scan(
		&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
		&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRow{}, fmt.Errorf("%w: session", ErrNotFound)
		}
		return SessionRow{}, err
	}
	if lastText.Valid {
		session.LastMessageText = &lastText.String
	}
	if lastAtMs.Valid {
		session.LastMessageAtMs = &lastAtMs.Int64
	}
	return session, nil
}

func (s *Store) GetSessionByID(ctx context.Context, sessionID string) (SessionRow, error) {
	if s == nil || s.db == nil {
		return SessionRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms
		FROM sessions WHERE id = ?;`

	var session SessionRow
	var lastText sql.NullString
	var lastAtMs sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(q), sessionID).Scan(
		&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
		&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRow{}, fmt.Errorf("%w: session", ErrNotFound)
		}
		return SessionRow{}, err
	}
	if lastText.Valid {
		session.LastMessageText = &lastText.String
	}
	if lastAtMs.Valid {
		session.LastMessageAtMs = &lastAtMs.Int64
	}
	return session, nil
}

func (s *Store) ListSessionsForUser(ctx context.Context, userID, status string) ([]SessionRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms
		FROM sessions
		WHERE (user1_id = ? OR user2_id = ?) AND status = ?
		ORDER BY updated_at_ms DESC;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), userID, userID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		var session SessionRow
		var lastText sql.NullString
		var lastAtMs sql.NullInt64
		if err := rows.Scan(
			&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
			&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if lastText.Valid {
			session.LastMessageText = &lastText.String
		}
		if lastAtMs.Valid {
			session.LastMessageAtMs = &lastAtMs.Int64
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return sessions, nil
}

func (s *Store) ArchiveSession(ctx context.Context, sessionID, userID string, nowMs int64) (SessionRow, error) {
	if s == nil || s.db == nil {
		return SessionRow{}, fmt.Errorf("db not initialized")
	}

	session, err := s.GetSessionByID(ctx, sessionID)
	if err != nil {
		return SessionRow{}, err
	}

	if session.User1ID != userID && session.User2ID != userID {
		return SessionRow{}, ErrAccessDenied
	}

	q := `UPDATE sessions SET status = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := s.db.ExecContext(ctx, s.rebind(q), SessionStatusArchived, nowMs, sessionID); err != nil {
		return SessionRow{}, err
	}

	session.Status = SessionStatusArchived
	session.UpdatedAtMs = nowMs
	return session, nil
}

func (s *Store) IsSessionParticipant(ctx context.Context, sessionID, userID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("db not initialized")
	}

	q := `SELECT 1 FROM sessions WHERE id = ? AND (user1_id = ? OR user2_id = ?);`
	var one int
	if err := s.db.QueryRowContext(ctx, s.rebind(q), sessionID, userID, userID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Store) GetPeerUserID(session SessionRow, currentUserID string) string {
	if session.User1ID == currentUserID {
		return session.User2ID
	}
	return session.User1ID
}
