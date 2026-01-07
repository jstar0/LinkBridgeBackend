package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

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
	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms, hidden_by_users, reactivated_at_ms
		FROM sessions WHERE participants_hash = ?;`

	var session SessionRow
	var lastText sql.NullString
	var lastAtMs sql.NullInt64
	var hiddenBy sql.NullString
	var reactivatedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(q), hash).Scan(
		&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
		&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
		&hiddenBy, &reactivatedAt,
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
	if hiddenBy.Valid {
		session.HiddenByUsers = &hiddenBy.String
	}
	if reactivatedAt.Valid {
		session.ReactivatedAtMs = &reactivatedAt.Int64
	}
	return session, nil
}

func (s *Store) GetSessionByID(ctx context.Context, sessionID string) (SessionRow, error) {
	if s == nil || s.db == nil {
		return SessionRow{}, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms, hidden_by_users, reactivated_at_ms
		FROM sessions WHERE id = ?;`

	var session SessionRow
	var lastText sql.NullString
	var lastAtMs sql.NullInt64
	var hiddenBy sql.NullString
	var reactivatedAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(q), sessionID).Scan(
		&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
		&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
		&hiddenBy, &reactivatedAt,
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
	if hiddenBy.Valid {
		session.HiddenByUsers = &hiddenBy.String
	}
	if reactivatedAt.Valid {
		session.ReactivatedAtMs = &reactivatedAt.Int64
	}
	return session, nil
}

func (s *Store) ListSessionsForUser(ctx context.Context, userID, status string) ([]SessionRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}

	q := `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms, hidden_by_users, reactivated_at_ms
		FROM sessions
		WHERE (user1_id = ? OR user2_id = ?) AND status = ?
		AND (hidden_by_users IS NULL OR hidden_by_users NOT LIKE '%' || ? || '%')
		ORDER BY updated_at_ms DESC;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), userID, userID, status, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		var session SessionRow
		var lastText sql.NullString
		var lastAtMs sql.NullInt64
		var hiddenBy sql.NullString
		var reactivatedAt sql.NullInt64
		if err := rows.Scan(
			&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
			&session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
			&hiddenBy, &reactivatedAt,
		); err != nil {
			return nil, err
		}
		if lastText.Valid {
			session.LastMessageText = &lastText.String
		}
		if lastAtMs.Valid {
			session.LastMessageAtMs = &lastAtMs.Int64
		}
		if hiddenBy.Valid {
			session.HiddenByUsers = &hiddenBy.String
		}
		if reactivatedAt.Valid {
			session.ReactivatedAtMs = &reactivatedAt.Int64
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

func (s *Store) getSessionByParticipants(ctx context.Context, user1ID, user2ID string) (SessionRow, error) {
	hash := computeParticipantsHash(user1ID, user2ID)
	return s.getSessionByHash(ctx, hash)
}

func createSessionInTx(ctx context.Context, tx *sql.Tx, driver, user1ID, user2ID string, nowMs int64) (SessionRow, error) {
	hash := computeParticipantsHash(user1ID, user2ID)

	// Check if session already exists
	selectQ := rebindQuery(driver, `SELECT id, participants_hash, user1_id, user2_id, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms, hidden_by_users, reactivated_at_ms
		FROM sessions WHERE participants_hash = ?;`)
	var existing SessionRow
	var lastText sql.NullString
	var lastAtMs sql.NullInt64
	var hiddenBy sql.NullString
	var reactivatedAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, selectQ, hash).Scan(
		&existing.ID, &existing.ParticipantsHash, &existing.User1ID, &existing.User2ID,
		&existing.Status, &lastText, &lastAtMs, &existing.CreatedAtMs, &existing.UpdatedAtMs,
		&hiddenBy, &reactivatedAt,
	); err == nil {
		if lastText.Valid {
			existing.LastMessageText = &lastText.String
		}
		if lastAtMs.Valid {
			existing.LastMessageAtMs = &lastAtMs.Int64
		}
		if hiddenBy.Valid {
			existing.HiddenByUsers = &hiddenBy.String
		}
		if reactivatedAt.Valid {
			existing.ReactivatedAtMs = &reactivatedAt.Int64
		}
		return existing, ErrSessionExists
	} else if err != sql.ErrNoRows {
		return SessionRow{}, err
	}

	ids := []string{user1ID, user2ID}
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

	insertQ := rebindQuery(driver, `INSERT INTO sessions (id, participants_hash, user1_id, user2_id, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?);`)
	if _, err := tx.ExecContext(ctx, insertQ,
		session.ID, session.ParticipantsHash, session.User1ID, session.User2ID,
		session.Status, nowMs, nowMs,
	); err != nil {
		return SessionRow{}, err
	}

	return session, nil
}

func (s *Store) ReactivateSession(ctx context.Context, sessionID, userID string, nowMs int64) (SessionRow, error) {
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

	if session.Status != SessionStatusArchived {
		return SessionRow{}, ErrInvalidState
	}

	q := `UPDATE sessions SET status = ?, reactivated_at_ms = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := s.db.ExecContext(ctx, s.rebind(q), SessionStatusActive, nowMs, nowMs, sessionID); err != nil {
		return SessionRow{}, err
	}

	session.Status = SessionStatusActive
	session.ReactivatedAtMs = &nowMs
	session.UpdatedAtMs = nowMs
	return session, nil
}

func (s *Store) HideSession(ctx context.Context, sessionID, userID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}

	session, err := s.GetSessionByID(ctx, sessionID)
	if err != nil {
		return err
	}

	if session.User1ID != userID && session.User2ID != userID {
		return ErrAccessDenied
	}

	// Parse existing hidden_by_users JSON array
	hiddenUsers := "[]"
	if session.HiddenByUsers != nil {
		hiddenUsers = *session.HiddenByUsers
	}

	// Simple string manipulation to add user ID to array
	// Format: ["user1","user2"]
	if hiddenUsers == "[]" {
		hiddenUsers = fmt.Sprintf("[\"%s\"]", userID)
	} else {
		// Check if user already in array
		if contains(hiddenUsers, userID) {
			return nil // Already hidden
		}
		// Insert before closing bracket
		hiddenUsers = hiddenUsers[:len(hiddenUsers)-1] + fmt.Sprintf(",\"%s\"]", userID)
	}

	q := `UPDATE sessions SET hidden_by_users = ? WHERE id = ?;`
	if _, err := s.db.ExecContext(ctx, s.rebind(q), hiddenUsers, sessionID); err != nil {
		return err
	}

	return nil
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func (s *Store) ReactivateSessionByParticipants(ctx context.Context, user1ID, user2ID string, nowMs int64) (SessionRow, error) {
	session, err := s.getSessionByParticipants(ctx, user1ID, user2ID)
	if err != nil {
		return SessionRow{}, err
	}
	if session.Status != SessionStatusArchived {
		return SessionRow{}, ErrInvalidState
	}
	return s.ReactivateSession(ctx, session.ID, user1ID, nowMs)
}

