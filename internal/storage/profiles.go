package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) GetUserCardProfile(ctx context.Context, userID string) (UserProfileRow, error) {
	return s.getUserProfile(ctx, "user_card_profiles", userID)
}

func (s *Store) UpsertUserCardProfile(ctx context.Context, userID string, nicknameOverride, avatarURLOverride *string, profileJSON string, nowMs int64) (UserProfileRow, error) {
	return s.upsertUserProfile(ctx, "user_card_profiles", userID, nicknameOverride, avatarURLOverride, profileJSON, nowMs)
}

func (s *Store) GetUserMapProfile(ctx context.Context, userID string) (UserProfileRow, error) {
	return s.getUserProfile(ctx, "user_map_profiles", userID)
}

func (s *Store) UpsertUserMapProfile(ctx context.Context, userID string, nicknameOverride, avatarURLOverride *string, profileJSON string, nowMs int64) (UserProfileRow, error) {
	return s.upsertUserProfile(ctx, "user_map_profiles", userID, nicknameOverride, avatarURLOverride, profileJSON, nowMs)
}

func (s *Store) getUserProfile(ctx context.Context, table, userID string) (UserProfileRow, error) {
	if s == nil || s.db == nil {
		return UserProfileRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return UserProfileRow{}, fmt.Errorf("missing userID")
	}
	if !isSafeIdentifier(table) {
		return UserProfileRow{}, fmt.Errorf("unsafe table name %q", table)
	}

	q := fmt.Sprintf(`SELECT user_id, nickname_override, avatar_url_override, profile_json, created_at_ms, updated_at_ms
		FROM %s WHERE user_id = ?;`, table)

	var row UserProfileRow
	var nick sql.NullString
	var avatar sql.NullString
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID).Scan(
		&row.UserID, &nick, &avatar, &row.ProfileJSON, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return UserProfileRow{}, fmt.Errorf("%w: %s", ErrNotFound, table)
		}
		return UserProfileRow{}, err
	}
	if nick.Valid {
		row.NicknameOverride = &nick.String
	}
	if avatar.Valid {
		row.AvatarURLOverride = &avatar.String
	}
	return row, nil
}

func (s *Store) upsertUserProfile(ctx context.Context, table, userID string, nicknameOverride, avatarURLOverride *string, profileJSON string, nowMs int64) (UserProfileRow, error) {
	if s == nil || s.db == nil {
		return UserProfileRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return UserProfileRow{}, fmt.Errorf("missing userID")
	}
	if !isSafeIdentifier(table) {
		return UserProfileRow{}, fmt.Errorf("unsafe table name %q", table)
	}

	profileJSON = strings.TrimSpace(profileJSON)
	if profileJSON == "" {
		profileJSON = "{}"
	}

	var nick sql.NullString
	if nicknameOverride != nil && strings.TrimSpace(*nicknameOverride) != "" {
		nick = sql.NullString{String: strings.TrimSpace(*nicknameOverride), Valid: true}
	}
	var avatar sql.NullString
	if avatarURLOverride != nil && strings.TrimSpace(*avatarURLOverride) != "" {
		avatar = sql.NullString{String: strings.TrimSpace(*avatarURLOverride), Valid: true}
	}

	q := fmt.Sprintf(`INSERT INTO %s (user_id, nickname_override, avatar_url_override, profile_json, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			nickname_override = excluded.nickname_override,
			avatar_url_override = excluded.avatar_url_override,
			profile_json = excluded.profile_json,
			updated_at_ms = excluded.updated_at_ms;`, table)

	if _, err := s.db.ExecContext(ctx, s.rebind(q), userID, nick, avatar, profileJSON, nowMs, nowMs); err != nil {
		return UserProfileRow{}, err
	}
	return s.getUserProfile(ctx, table, userID)
}
