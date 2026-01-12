package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	minBurnAfterMs = int64(1000)                     // 1s
	maxBurnAfterMs = int64(30 * 24 * 60 * 60 * 1000) // 30d
)

func (s *Store) CreateBurnMessage(ctx context.Context, sessionID, senderID string, metaJSON []byte, burnAfterMs int64, nowMs int64) (MessageRow, BurnMessageRow, error) {
	if s == nil || s.db == nil {
		return MessageRow{}, BurnMessageRow{}, fmt.Errorf("db not initialized")
	}
	sessionID = strings.TrimSpace(sessionID)
	senderID = strings.TrimSpace(senderID)
	if sessionID == "" || senderID == "" {
		return MessageRow{}, BurnMessageRow{}, fmt.Errorf("missing required fields")
	}

	if burnAfterMs < minBurnAfterMs || burnAfterMs > maxBurnAfterMs {
		return MessageRow{}, BurnMessageRow{}, fmt.Errorf("invalid burnAfterMs")
	}

	metaJSON = bytesTrimSpace(metaJSON)
	if len(metaJSON) == 0 {
		return MessageRow{}, BurnMessageRow{}, fmt.Errorf("missing metaJSON")
	}
	if err := validateJSONObject(metaJSON); err != nil {
		return MessageRow{}, BurnMessageRow{}, fmt.Errorf("invalid metaJSON: %w", err)
	}

	session, err := s.GetSessionByID(ctx, sessionID)
	if err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}
	if session.Kind != SessionKindDirect {
		return MessageRow{}, BurnMessageRow{}, ErrInvalidState
	}
	if session.User1ID != senderID && session.User2ID != senderID {
		return MessageRow{}, BurnMessageRow{}, ErrAccessDenied
	}
	if session.Status == SessionStatusArchived {
		return MessageRow{}, BurnMessageRow{}, ErrSessionArchived
	}

	recipientID := s.GetPeerUserID(session, senderID)
	if strings.TrimSpace(recipientID) == "" {
		return MessageRow{}, BurnMessageRow{}, ErrAccessDenied
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	messageID := uuid.NewString()
	insertMsgQ := `INSERT INTO messages (id, session_id, sender_id, type, text, meta_json, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?);`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertMsgQ),
		messageID, sessionID, senderID, MessageTypeBurn, nil, string(metaJSON), nowMs,
	); err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}

	burnRow := BurnMessageRow{
		MessageID:   messageID,
		SessionID:   sessionID,
		SenderID:    senderID,
		RecipientID: recipientID,
		BurnAfterMs: burnAfterMs,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	insertBurnQ := `INSERT INTO burn_messages (
			message_id, session_id, sender_id, recipient_id, burn_after_ms, opened_at_ms, burn_at_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertBurnQ),
		burnRow.MessageID, burnRow.SessionID, burnRow.SenderID, burnRow.RecipientID,
		burnRow.BurnAfterMs, nil, nil, burnRow.CreatedAtMs, burnRow.UpdatedAtMs,
	); err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}

	// Server can't preview encrypted content.
	lastMessageText := "[阅后即焚]"
	updateSessQ := `UPDATE sessions SET last_message_text = ?, last_message_at_ms = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, updateSessQ),
		lastMessageText, nowMs, nowMs, sessionID,
	); err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return MessageRow{}, BurnMessageRow{}, err
	}

	msg := MessageRow{
		ID:          messageID,
		SessionID:   sessionID,
		SenderID:    senderID,
		Type:        MessageTypeBurn,
		Text:        nil,
		MetaJSON:    metaJSON,
		CreatedAtMs: nowMs,
	}
	return msg, burnRow, nil
}

func (s *Store) GetBurnMessages(ctx context.Context, messageIDs []string) (map[string]BurnMessageRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if len(messageIDs) == 0 {
		return map[string]BurnMessageRow{}, nil
	}

	args := make([]any, 0, len(messageIDs))
	for _, id := range messageIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		args = append(args, id)
	}
	if len(args) == 0 {
		return map[string]BurnMessageRow{}, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(args)), ",")
	q := fmt.Sprintf(`SELECT
			message_id, session_id, sender_id, recipient_id, burn_after_ms, opened_at_ms, burn_at_ms, created_at_ms, updated_at_ms
		FROM burn_messages
		WHERE message_id IN (%s);`, placeholders)

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]BurnMessageRow, len(args))
	for rows.Next() {
		var row BurnMessageRow
		var opened sql.NullInt64
		var burnAt sql.NullInt64
		if err := rows.Scan(
			&row.MessageID, &row.SessionID, &row.SenderID, &row.RecipientID,
			&row.BurnAfterMs, &opened, &burnAt, &row.CreatedAtMs, &row.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if opened.Valid {
			row.OpenedAtMs = &opened.Int64
		}
		if burnAt.Valid {
			row.BurnAtMs = &burnAt.Int64
		}
		out[row.MessageID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MarkBurnMessageRead(ctx context.Context, messageID, userID string, nowMs int64) (BurnMessageRow, bool, error) {
	if s == nil || s.db == nil {
		return BurnMessageRow{}, false, fmt.Errorf("db not initialized")
	}
	messageID = strings.TrimSpace(messageID)
	userID = strings.TrimSpace(userID)
	if messageID == "" || userID == "" {
		return BurnMessageRow{}, false, fmt.Errorf("missing required fields")
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return BurnMessageRow{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	row, err := getBurnMessageInTx(txCtx, tx, s.driver, messageID)
	if err != nil {
		return BurnMessageRow{}, false, err
	}
	if row.RecipientID != userID {
		return BurnMessageRow{}, false, ErrAccessDenied
	}

	if row.OpenedAtMs != nil && row.BurnAtMs != nil {
		return row, false, nil
	}

	burnAtMs := nowMs + row.BurnAfterMs
	updateQ := `UPDATE burn_messages
		SET opened_at_ms = ?, burn_at_ms = ?, updated_at_ms = ?
		WHERE message_id = ? AND opened_at_ms IS NULL;`
	res, err := tx.ExecContext(txCtx, rebindQuery(s.driver, updateQ), nowMs, burnAtMs, nowMs, messageID)
	if err != nil {
		return BurnMessageRow{}, false, err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		current, err := getBurnMessageInTx(txCtx, tx, s.driver, messageID)
		if err != nil {
			return BurnMessageRow{}, false, err
		}
		if err := tx.Commit(); err != nil {
			return BurnMessageRow{}, false, err
		}
		return current, false, nil
	}

	row.OpenedAtMs = &nowMs
	row.BurnAtMs = &burnAtMs
	row.UpdatedAtMs = nowMs

	if err := tx.Commit(); err != nil {
		return BurnMessageRow{}, false, err
	}
	return row, true, nil
}

func (s *Store) ExpireBurnMessages(ctx context.Context, nowMs int64, limit int) ([]BurnMessageRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	selectQ := `SELECT message_id, session_id, sender_id, recipient_id
		FROM burn_messages
		WHERE burn_at_ms IS NOT NULL AND burn_at_ms <= ?
		ORDER BY burn_at_ms ASC
		LIMIT ?;`
	rows, err := tx.QueryContext(txCtx, rebindQuery(s.driver, selectQ), nowMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var due []BurnMessageRow
	var msgIDs []any
	for rows.Next() {
		var row BurnMessageRow
		if err := rows.Scan(&row.MessageID, &row.SessionID, &row.SenderID, &row.RecipientID); err != nil {
			return nil, err
		}
		due = append(due, row)
		msgIDs = append(msgIDs, row.MessageID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(due) == 0 {
		return nil, nil
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(msgIDs)), ",")
	deleteQ := fmt.Sprintf(`DELETE FROM messages WHERE id IN (%s);`, placeholders)
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, deleteQ), msgIDs...); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return due, nil
}

func getBurnMessageInTx(ctx context.Context, tx *sql.Tx, driver, messageID string) (BurnMessageRow, error) {
	q := rebindQuery(driver, `SELECT
			message_id, session_id, sender_id, recipient_id, burn_after_ms, opened_at_ms, burn_at_ms, created_at_ms, updated_at_ms
		FROM burn_messages WHERE message_id = ?;`)
	var row BurnMessageRow
	var opened sql.NullInt64
	var burnAt sql.NullInt64
	if err := tx.QueryRowContext(ctx, q, messageID).Scan(
		&row.MessageID, &row.SessionID, &row.SenderID, &row.RecipientID,
		&row.BurnAfterMs, &opened, &burnAt, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BurnMessageRow{}, fmt.Errorf("%w: burn message", ErrNotFound)
		}
		return BurnMessageRow{}, err
	}
	if opened.Valid {
		row.OpenedAtMs = &opened.Int64
	}
	if burnAt.Valid {
		row.BurnAtMs = &burnAt.Int64
	}
	return row, nil
}

func validateJSONObject(raw []byte) error {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	if v == nil {
		return errors.New("expected JSON object")
	}
	if _, ok := v.(map[string]any); !ok {
		return errors.New("expected JSON object")
	}
	return nil
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
