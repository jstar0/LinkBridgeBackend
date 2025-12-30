package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

type MessageMeta struct {
	Name      string `json:"name,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	URL       string `json:"url,omitempty"`
}

func (s *Store) ListMessages(ctx context.Context, sessionID, userID string, limit int, beforeID string) ([]MessageRow, bool, error) {
	if s == nil || s.db == nil {
		return nil, false, fmt.Errorf("db not initialized")
	}

	isParticipant, err := s.IsSessionParticipant(ctx, sessionID, userID)
	if err != nil {
		return nil, false, err
	}
	if !isParticipant {
		return nil, false, ErrAccessDenied
	}

	if limit <= 0 {
		limit = 50
	}

	var q string
	var args []any

	if beforeID != "" {
		var beforeCreatedAt int64
		subQ := `SELECT created_at_ms FROM messages WHERE id = ?;`
		if err := s.db.QueryRowContext(ctx, s.rebind(subQ), beforeID).Scan(&beforeCreatedAt); err != nil {
			if err == sql.ErrNoRows {
				return nil, false, fmt.Errorf("%w: message", ErrNotFound)
			}
			return nil, false, err
		}

		q = `SELECT id, session_id, sender_id, type, text, meta_json, created_at_ms
			FROM messages
			WHERE session_id = ? AND created_at_ms < ?
			ORDER BY created_at_ms DESC
			LIMIT ?;`
		args = []any{sessionID, beforeCreatedAt, limit + 1}
	} else {
		q = `SELECT id, session_id, sender_id, type, text, meta_json, created_at_ms
			FROM messages
			WHERE session_id = ?
			ORDER BY created_at_ms DESC
			LIMIT ?;`
		args = []any{sessionID, limit + 1}
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()

	var messages []MessageRow
	for rows.Next() {
		var text sql.NullString
		var meta sql.NullString
		var mrow MessageRow
		if err := rows.Scan(&mrow.ID, &mrow.SessionID, &mrow.SenderID, &mrow.Type, &text, &meta, &mrow.CreatedAtMs); err != nil {
			return nil, false, err
		}
		if text.Valid {
			mrow.Text = &text.String
		}
		if meta.Valid && meta.String != "" {
			mrow.MetaJSON = []byte(meta.String)
		}
		messages = append(messages, mrow)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, hasMore, nil
}

func (s *Store) CreateMessage(ctx context.Context, sessionID, senderID, msgType string, text *string, meta *MessageMeta, nowMs int64) (MessageRow, error) {
	if s == nil || s.db == nil {
		return MessageRow{}, fmt.Errorf("db not initialized")
	}

	isParticipant, err := s.IsSessionParticipant(ctx, sessionID, senderID)
	if err != nil {
		return MessageRow{}, err
	}
	if !isParticipant {
		return MessageRow{}, ErrAccessDenied
	}

	metaJSON, err := marshalMeta(meta)
	if err != nil {
		return MessageRow{}, err
	}

	messageID := uuid.NewString()
	lastMessageText := buildLastMessageText(msgType, text, meta)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return MessageRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	insertQ := `INSERT INTO messages (id, session_id, sender_id, type, text, meta_json, created_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?);`

	var textVal any
	if text != nil {
		textVal = *text
	}

	var metaVal any
	if len(metaJSON) > 0 {
		metaVal = string(metaJSON)
	}

	if _, err := tx.ExecContext(ctx, s.rebind(insertQ),
		messageID, sessionID, senderID, msgType, textVal, metaVal, nowMs,
	); err != nil {
		return MessageRow{}, err
	}

	updateQ := `UPDATE sessions SET last_message_text = ?, last_message_at_ms = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := tx.ExecContext(ctx, s.rebind(updateQ), lastMessageText, nowMs, nowMs, sessionID); err != nil {
		return MessageRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return MessageRow{}, err
	}

	msg := MessageRow{
		ID:          messageID,
		SessionID:   sessionID,
		SenderID:    senderID,
		Type:        msgType,
		Text:        text,
		MetaJSON:    metaJSON,
		CreatedAtMs: nowMs,
	}
	return msg, nil
}

func marshalMeta(meta *MessageMeta) ([]byte, error) {
	if meta == nil {
		return nil, nil
	}
	if meta.Name == "" && meta.SizeBytes == 0 {
		return nil, nil
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func buildLastMessageText(msgType string, text *string, meta *MessageMeta) string {
	if msgType == MessageTypeText {
		if text != nil {
			return *text
		}
		return ""
	}
	if meta != nil && meta.Name != "" {
		return "[" + msgType + "] " + meta.Name
	}
	return "[" + msgType + "]"
}
