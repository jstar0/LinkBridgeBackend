package storage

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *Store) UpsertWeChatBinding(ctx context.Context, userID, openID, sessionKey string, unionID *string, nowMs int64) (WeChatBindingRow, error) {
	if s == nil || s.db == nil {
		return WeChatBindingRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" || openID == "" || sessionKey == "" {
		return WeChatBindingRow{}, fmt.Errorf("missing required fields")
	}

	var union sql.NullString
	if unionID != nil && *unionID != "" {
		union = sql.NullString{String: *unionID, Valid: true}
	}

	q := `INSERT INTO wechat_bindings (user_id, openid, session_key, unionid, updated_at_ms)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			openid = excluded.openid,
			session_key = excluded.session_key,
			unionid = excluded.unionid,
			updated_at_ms = excluded.updated_at_ms;`

	if _, err := s.db.ExecContext(ctx, s.rebind(q), userID, openID, sessionKey, union, nowMs); err != nil {
		return WeChatBindingRow{}, err
	}

	return WeChatBindingRow{
		UserID:      userID,
		OpenID:      openID,
		SessionKey:  sessionKey,
		UnionID:     unionID,
		UpdatedAtMs: nowMs,
	}, nil
}

func (s *Store) GetWeChatBindingByUserID(ctx context.Context, userID string) (WeChatBindingRow, error) {
	if s == nil || s.db == nil {
		return WeChatBindingRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return WeChatBindingRow{}, fmt.Errorf("missing userID")
	}

	q := `SELECT user_id, openid, session_key, unionid, updated_at_ms FROM wechat_bindings WHERE user_id = ?;`

	var row WeChatBindingRow
	var union sql.NullString
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID).Scan(&row.UserID, &row.OpenID, &row.SessionKey, &union, &row.UpdatedAtMs); err != nil {
		if err == sql.ErrNoRows {
			return WeChatBindingRow{}, fmt.Errorf("%w: wechat binding", ErrNotFound)
		}
		return WeChatBindingRow{}, err
	}
	if union.Valid {
		row.UnionID = &union.String
	}
	return row, nil
}
