package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) GetSessionUserMeta(ctx context.Context, sessionID, userID string) (SessionUserMetaRow, error) {
	if s == nil || s.db == nil {
		return SessionUserMetaRow{}, fmt.Errorf("db not initialized")
	}
	if sessionID == "" || userID == "" {
		return SessionUserMetaRow{}, fmt.Errorf("missing ids")
	}

	q := `SELECT
			m.session_id,
			m.user_id,
			m.note,
			m.group_id,
			g.name,
			m.tags_json,
			m.created_at_ms,
			m.updated_at_ms
		FROM session_user_meta m
		LEFT JOIN relationship_groups g ON g.id = m.group_id
		WHERE m.session_id = ? AND m.user_id = ?;`

	var (
		row       SessionUserMetaRow
		note      sql.NullString
		groupID   sql.NullString
		groupName sql.NullString
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(q), sessionID, userID).Scan(
		&row.SessionID, &row.UserID, &note, &groupID, &groupName, &row.TagsJSON, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SessionUserMetaRow{}, fmt.Errorf("%w: session user meta", ErrNotFound)
		}
		return SessionUserMetaRow{}, err
	}
	if note.Valid {
		row.Note = &note.String
	}
	if groupID.Valid {
		row.GroupID = &groupID.String
	}
	if groupName.Valid {
		row.GroupName = &groupName.String
	}
	if strings.TrimSpace(row.TagsJSON) == "" {
		row.TagsJSON = "[]"
	}
	return row, nil
}

func (s *Store) UpsertSessionUserMeta(ctx context.Context, sessionID, userID string, note *string, groupID *string, tags []string, nowMs int64) (SessionUserMetaRow, error) {
	if s == nil || s.db == nil {
		return SessionUserMetaRow{}, fmt.Errorf("db not initialized")
	}
	if sessionID == "" || userID == "" {
		return SessionUserMetaRow{}, fmt.Errorf("missing ids")
	}

	normalizedNote := normalizeNote(note)
	normalizedGroup := normalizeNullableID(groupID)
	tagsJSON, err := normalizeTagsJSON(tags)
	if err != nil {
		return SessionUserMetaRow{}, err
	}

	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return SessionUserMetaRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	insertQ := `INSERT INTO session_user_meta (session_id, user_id, note, group_id, tags_json, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, user_id) DO UPDATE SET
			note = excluded.note,
			group_id = excluded.group_id,
			tags_json = excluded.tags_json,
			updated_at_ms = excluded.updated_at_ms;`

	var noteVal sql.NullString
	if normalizedNote != nil {
		noteVal = sql.NullString{String: *normalizedNote, Valid: true}
	}
	var groupVal sql.NullString
	if normalizedGroup != nil {
		groupVal = sql.NullString{String: *normalizedGroup, Valid: true}
	}

	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertQ), sessionID, userID, noteVal, groupVal, tagsJSON, nowMs, nowMs); err != nil {
		return SessionUserMetaRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return SessionUserMetaRow{}, err
	}

	return s.GetSessionUserMeta(ctx, sessionID, userID)
}

func insertDefaultSessionUserMetaIfMissing(ctx context.Context, tx *sql.Tx, driver, sessionID, userID string, groupID *string, nowMs int64) error {
	if sessionID == "" || userID == "" {
		return fmt.Errorf("missing ids")
	}

	var groupVal sql.NullString
	if groupID != nil && strings.TrimSpace(*groupID) != "" {
		groupVal = sql.NullString{String: strings.TrimSpace(*groupID), Valid: true}
	}

	q := rebindQuery(driver, `INSERT INTO session_user_meta (session_id, user_id, note, group_id, tags_json, created_at_ms, updated_at_ms)
		VALUES (?, ?, NULL, ?, '[]', ?, ?)
		ON CONFLICT(session_id, user_id) DO NOTHING;`)
	_, err := tx.ExecContext(ctx, q, sessionID, userID, groupVal, nowMs, nowMs)
	return err
}

func normalizeNote(note *string) *string {
	if note == nil {
		return nil
	}
	v := strings.TrimSpace(*note)
	if v == "" {
		return nil
	}
	if len(v) > 80 {
		v = v[:80]
	}
	return &v
}

func normalizeNullableID(id *string) *string {
	if id == nil {
		return nil
	}
	v := strings.TrimSpace(*id)
	if v == "" {
		return nil
	}
	return &v
}

func normalizeTagsJSON(tags []string) (string, error) {
	normalized := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if len(t) > 20 {
			t = t[:20]
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, t)
	}
	sort.Strings(normalized)
	if len(normalized) > 30 {
		normalized = normalized[:30]
	}

	b, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ParseTagsJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
