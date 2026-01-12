package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	SessionParticipantRoleCreator = "creator"
	SessionParticipantRoleAdmin   = "admin"
	SessionParticipantRoleMember  = "member"
)

const (
	SessionParticipantStatusActive  = "active"
	SessionParticipantStatusLeft    = "left"
	SessionParticipantStatusRemoved = "removed"
)

func (s *Store) CreateActivity(ctx context.Context, creatorID, title string, description *string, startAtMs, endAtMs *int64, nowMs int64) (ActivityRow, ActivityInviteRow, error) {
	if s == nil || s.db == nil {
		return ActivityRow{}, ActivityInviteRow{}, fmt.Errorf("db not initialized")
	}
	creatorID = strings.TrimSpace(creatorID)
	title = strings.TrimSpace(title)
	if creatorID == "" || title == "" {
		return ActivityRow{}, ActivityInviteRow{}, fmt.Errorf("missing required fields")
	}
	if len(title) > 50 {
		return ActivityRow{}, ActivityInviteRow{}, fmt.Errorf("title too long")
	}

	desc := normalizeOptionalText(description, 500)
	if startAtMs != nil && *startAtMs <= 0 {
		startAtMs = nil
	}
	if endAtMs != nil && *endAtMs <= 0 {
		endAtMs = nil
	}
	if endAtMs != nil && *endAtMs <= nowMs {
		return ActivityRow{}, ActivityInviteRow{}, fmt.Errorf("endAtMs must be in the future")
	}
	if startAtMs != nil && endAtMs != nil && *endAtMs <= *startAtMs {
		return ActivityRow{}, ActivityInviteRow{}, fmt.Errorf("endAtMs must be greater than startAtMs")
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	sessionID := uuid.NewString()
	session := SessionRow{
		ID:               sessionID,
		ParticipantsHash: uuid.NewString(),
		User1ID:          creatorID,
		User2ID:          creatorID,
		Source:           SessionSourceActivity,
		Kind:             SessionKindGroup,
		Status:           SessionStatusActive,
		CreatedAtMs:      nowMs,
		UpdatedAtMs:      nowMs,
	}

	insertSessionQ := `INSERT INTO sessions (
			id, participants_hash, user1_id, user2_id, source, kind, status, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertSessionQ),
		session.ID, session.ParticipantsHash, session.User1ID, session.User2ID,
		session.Source, session.Kind, session.Status, session.CreatedAtMs, session.UpdatedAtMs,
	); err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}

	if _, err := upsertSessionParticipantInTx(txCtx, tx, s.driver, session.ID, creatorID, SessionParticipantRoleCreator, SessionParticipantStatusActive, nowMs); err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}

	activity := ActivityRow{
		ID:          sessionID,
		SessionID:   sessionID,
		CreatorID:   creatorID,
		Title:       title,
		Description: desc,
		StartAtMs:   startAtMs,
		EndAtMs:     endAtMs,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	insertActivityQ := `INSERT INTO activities (id, session_id, creator_id, title, description, start_at_ms, end_at_ms, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`
	var descVal any
	if activity.Description != nil {
		descVal = *activity.Description
	}
	var startVal any
	if activity.StartAtMs != nil {
		startVal = *activity.StartAtMs
	}
	var endVal any
	if activity.EndAtMs != nil {
		endVal = *activity.EndAtMs
	}
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertActivityQ),
		activity.ID, activity.SessionID, activity.CreatorID, activity.Title, descVal, startVal, endVal, activity.CreatedAtMs, activity.UpdatedAtMs,
	); err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}

	invite, err := getOrCreateActivityInviteInTx(txCtx, tx, s.driver, activity.ID, nowMs)
	if err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}

	// Default grouping for activity relationships (only-if-missing).
	const defaultActivityGroupName = "活动"
	creatorGroup, err := getOrCreateRelationshipGroupByNameInTx(txCtx, tx, s.driver, creatorID, defaultActivityGroupName, nowMs)
	if err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}
	if err := insertDefaultSessionUserMetaIfMissing(txCtx, tx, s.driver, session.ID, creatorID, &creatorGroup.ID, nowMs); err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return ActivityRow{}, ActivityInviteRow{}, err
	}
	return activity, invite, nil
}

func (s *Store) GetActivityByID(ctx context.Context, activityID string) (ActivityRow, error) {
	if s == nil || s.db == nil {
		return ActivityRow{}, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return ActivityRow{}, fmt.Errorf("missing activityID")
	}

	q := `SELECT id, session_id, creator_id, title, description, start_at_ms, end_at_ms, created_at_ms, updated_at_ms
		FROM activities WHERE id = ?;`
	var (
		row   ActivityRow
		desc  sql.NullString
		start sql.NullInt64
		end   sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(q), activityID).Scan(
		&row.ID, &row.SessionID, &row.CreatorID, &row.Title, &desc, &start, &end, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActivityRow{}, fmt.Errorf("%w: activity", ErrNotFound)
		}
		return ActivityRow{}, err
	}
	if desc.Valid {
		row.Description = &desc.String
	}
	if start.Valid {
		row.StartAtMs = &start.Int64
	}
	if end.Valid {
		row.EndAtMs = &end.Int64
	}
	return row, nil
}

func (s *Store) GetOrCreateActivityInvite(ctx context.Context, activityID string, nowMs int64) (ActivityInviteRow, bool, error) {
	if s == nil || s.db == nil {
		return ActivityInviteRow{}, false, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return ActivityInviteRow{}, false, fmt.Errorf("missing activityID")
	}

	// Reuse an existing code to keep the WeChat code stable for an activity.
	const selectQ = `SELECT
			code,
			activity_id,
			expires_at_ms,
			geo_fence_lat_e7,
			geo_fence_lng_e7,
			geo_fence_radius_m,
			created_at_ms,
			updated_at_ms
		FROM activity_invites WHERE activity_id = ?;`
	var existing ActivityInviteRow
	var (
		expires sql.NullInt64
		gfLat   sql.NullInt64
		gfLng   sql.NullInt64
		gfRad   sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(selectQ), activityID).Scan(
		&existing.Code, &existing.ActivityID, &expires, &gfLat, &gfLng, &gfRad, &existing.CreatedAtMs, &existing.UpdatedAtMs,
	); err == nil {
		if expires.Valid && expires.Int64 > 0 {
			existing.ExpiresAtMs = &expires.Int64
		}
		if gfLat.Valid && gfLng.Valid && gfRad.Valid && gfRad.Int64 > 0 {
			existing.GeoFence = &GeoFence{LatE7: gfLat.Int64, LngE7: gfLng.Int64, RadiusM: int(gfRad.Int64)}
		}
		return existing, false, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ActivityInviteRow{}, false, err
	}

	for i := 0; i < 3; i++ {
		code, err := newInviteCode(8) // 16 hex chars
		if err != nil {
			return ActivityInviteRow{}, false, err
		}
		row := ActivityInviteRow{
			Code:        code,
			ActivityID:  activityID,
			CreatedAtMs: nowMs,
			UpdatedAtMs: nowMs,
		}
		const insertQ = `INSERT INTO activity_invites (
				code, activity_id, expires_at_ms, geo_fence_lat_e7, geo_fence_lng_e7, geo_fence_radius_m, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?);`
		if _, err := s.db.ExecContext(ctx, s.rebind(insertQ),
			row.Code, row.ActivityID, nil, nil, nil, nil, row.CreatedAtMs, row.UpdatedAtMs,
		); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return ActivityInviteRow{}, false, err
		}
		return row, true, nil
	}
	return ActivityInviteRow{}, false, fmt.Errorf("failed to create invite code")
}

func (s *Store) UpdateActivityInviteSettings(ctx context.Context, activityID string, expiresAtMs *int64, geoFence *GeoFence, nowMs int64) (ActivityInviteRow, error) {
	if s == nil || s.db == nil {
		return ActivityInviteRow{}, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return ActivityInviteRow{}, fmt.Errorf("missing activityID")
	}

	// Ensure invite exists (stable code).
	if _, _, err := s.GetOrCreateActivityInvite(ctx, activityID, nowMs); err != nil {
		return ActivityInviteRow{}, err
	}

	var exp any
	if expiresAtMs != nil && *expiresAtMs > 0 {
		exp = *expiresAtMs
	}

	var lat any
	var lng any
	var rad any
	if geoFence != nil && geoFence.RadiusM > 0 {
		lat = geoFence.LatE7
		lng = geoFence.LngE7
		rad = geoFence.RadiusM
	}

	q := `UPDATE activity_invites
		SET expires_at_ms = ?, geo_fence_lat_e7 = ?, geo_fence_lng_e7 = ?, geo_fence_radius_m = ?, updated_at_ms = ?
		WHERE activity_id = ?;`
	if _, err := s.db.ExecContext(ctx, s.rebind(q), exp, lat, lng, rad, nowMs, activityID); err != nil {
		return ActivityInviteRow{}, err
	}

	row, _, err := s.GetOrCreateActivityInvite(ctx, activityID, nowMs)
	return row, err
}

func (s *Store) ResolveActivityInvite(ctx context.Context, code string) (ActivityInviteRow, error) {
	if s == nil || s.db == nil {
		return ActivityInviteRow{}, fmt.Errorf("db not initialized")
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return ActivityInviteRow{}, fmt.Errorf("missing code")
	}

	const q = `SELECT
			code,
			activity_id,
			expires_at_ms,
			geo_fence_lat_e7,
			geo_fence_lng_e7,
			geo_fence_radius_m,
			created_at_ms,
			updated_at_ms
		FROM activity_invites WHERE code = ?;`
	var row ActivityInviteRow
	var (
		expires sql.NullInt64
		gfLat   sql.NullInt64
		gfLng   sql.NullInt64
		gfRad   sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(q), code).Scan(
		&row.Code, &row.ActivityID, &expires, &gfLat, &gfLng, &gfRad, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActivityInviteRow{}, ErrInviteInvalid
		}
		return ActivityInviteRow{}, err
	}
	if expires.Valid && expires.Int64 > 0 {
		row.ExpiresAtMs = &expires.Int64
	}
	if gfLat.Valid && gfLng.Valid && gfRad.Valid && gfRad.Int64 > 0 {
		row.GeoFence = &GeoFence{LatE7: gfLat.Int64, LngE7: gfLng.Int64, RadiusM: int(gfRad.Int64)}
	}
	return row, nil
}

func (s *Store) ConsumeActivityInvite(ctx context.Context, userID, code string, atLatE7, atLngE7 *int64, nowMs int64) (ActivityRow, SessionRow, bool, error) {
	if s == nil || s.db == nil {
		return ActivityRow{}, SessionRow{}, false, fmt.Errorf("db not initialized")
	}
	userID = strings.TrimSpace(userID)
	code = strings.TrimSpace(code)
	if userID == "" || code == "" {
		return ActivityRow{}, SessionRow{}, false, fmt.Errorf("missing required fields")
	}

	invite, err := s.ResolveActivityInvite(ctx, code)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}

	if invite.ExpiresAtMs != nil && nowMs > *invite.ExpiresAtMs {
		return ActivityRow{}, SessionRow{}, false, ErrInviteExpired
	}
	if invite.GeoFence != nil && invite.GeoFence.RadiusM > 0 {
		if atLatE7 == nil || atLngE7 == nil {
			return ActivityRow{}, SessionRow{}, false, ErrGeoFenceRequired
		}
		dist := distanceMetersE7(invite.GeoFence.LatE7, invite.GeoFence.LngE7, *atLatE7, *atLngE7)
		if dist > float64(invite.GeoFence.RadiusM) {
			return ActivityRow{}, SessionRow{}, false, ErrGeoFenceForbidden
		}
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	activity, err := getActivityByIDInTx(txCtx, tx, s.driver, invite.ActivityID)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}

	// Reject joining expired activities.
	if activity.EndAtMs != nil && nowMs > *activity.EndAtMs {
		return ActivityRow{}, SessionRow{}, false, ErrInvalidState
	}

	session, err := getSessionByIDInTx(txCtx, tx, s.driver, activity.SessionID)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}
	if session.Status != SessionStatusActive {
		return ActivityRow{}, SessionRow{}, false, ErrSessionArchived
	}

	created, err := upsertSessionParticipantInTx(txCtx, tx, s.driver, session.ID, userID, SessionParticipantRoleMember, SessionParticipantStatusActive, nowMs)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}

	// Default grouping for activity relationships (only-if-missing).
	const defaultActivityGroupName = "活动"
	group, err := getOrCreateRelationshipGroupByNameInTx(txCtx, tx, s.driver, userID, defaultActivityGroupName, nowMs)
	if err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}
	if err := insertDefaultSessionUserMetaIfMissing(txCtx, tx, s.driver, session.ID, userID, &group.ID, nowMs); err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return ActivityRow{}, SessionRow{}, false, err
	}

	return activity, session, created, nil
}

func (s *Store) ListActivityMembers(ctx context.Context, activityID string) ([]SessionParticipantRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return nil, fmt.Errorf("missing activityID")
	}

	activity, err := s.GetActivityByID(ctx, activityID)
	if err != nil {
		return nil, err
	}

	q := `SELECT session_id, user_id, role, status, created_at_ms, updated_at_ms
		FROM session_participants
		WHERE session_id = ?
		ORDER BY role ASC, created_at_ms ASC;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), activity.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SessionParticipantRow
	for rows.Next() {
		var p SessionParticipantRow
		if err := rows.Scan(&p.SessionID, &p.UserID, &p.Role, &p.Status, &p.CreatedAtMs, &p.UpdatedAtMs); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) RemoveActivityMember(ctx context.Context, activityID, actorUserID, targetUserID string, nowMs int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	actorUserID = strings.TrimSpace(actorUserID)
	targetUserID = strings.TrimSpace(targetUserID)
	if activityID == "" || actorUserID == "" || targetUserID == "" {
		return fmt.Errorf("missing required fields")
	}

	activity, err := s.GetActivityByID(ctx, activityID)
	if err != nil {
		return err
	}
	if activity.CreatorID != actorUserID {
		return ErrAccessDenied
	}
	if targetUserID == activity.CreatorID {
		return ErrAccessDenied
	}

	q := `UPDATE session_participants
		SET status = ?, updated_at_ms = ?
		WHERE session_id = ? AND user_id = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), SessionParticipantStatusRemoved, nowMs, activity.SessionID, targetUserID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: session participant", ErrNotFound)
	}
	return nil
}

func (s *Store) ExtendActivity(ctx context.Context, activityID, actorUserID string, newEndAtMs int64, nowMs int64) (ActivityRow, error) {
	if s == nil || s.db == nil {
		return ActivityRow{}, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	actorUserID = strings.TrimSpace(actorUserID)
	if activityID == "" || actorUserID == "" {
		return ActivityRow{}, fmt.Errorf("missing required fields")
	}
	if newEndAtMs <= nowMs {
		return ActivityRow{}, fmt.Errorf("newEndAtMs must be in the future")
	}

	txCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return ActivityRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	activity, err := getActivityByIDInTx(txCtx, tx, s.driver, activityID)
	if err != nil {
		return ActivityRow{}, err
	}
	if activity.CreatorID != actorUserID {
		return ActivityRow{}, ErrAccessDenied
	}

	updateActivityQ := `UPDATE activities SET end_at_ms = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, updateActivityQ), newEndAtMs, nowMs, activityID); err != nil {
		return ActivityRow{}, err
	}

	// Reactivate the underlying group session if it was archived.
	updateSessionQ := `UPDATE sessions SET status = ?, updated_at_ms = ? WHERE id = ?;`
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, updateSessionQ), SessionStatusActive, nowMs, activity.SessionID); err != nil {
		return ActivityRow{}, err
	}

	if err := tx.Commit(); err != nil {
		return ActivityRow{}, err
	}

	return s.GetActivityByID(ctx, activityID)
}

func (s *Store) ListActivitiesForUser(ctx context.Context, userID, status string, nowMs int64, limit int) ([]ActivityRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	_ = nowMs
	userID = strings.TrimSpace(userID)
	status = strings.TrimSpace(status)
	if userID == "" {
		return nil, fmt.Errorf("missing userID")
	}
	if status != SessionStatusActive && status != SessionStatusArchived {
		status = SessionStatusActive
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}

	q := `SELECT
			a.id,
			a.session_id,
			a.creator_id,
			a.title,
			a.description,
			a.start_at_ms,
			a.end_at_ms,
			a.created_at_ms,
			a.updated_at_ms
		FROM activities a
		JOIN sessions s ON s.id = a.session_id
		JOIN session_participants p ON p.session_id = a.session_id AND p.user_id = ? AND p.status = ?
		WHERE s.status = ?
		ORDER BY a.updated_at_ms DESC
		LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), userID, SessionParticipantStatusActive, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ActivityRow
	for rows.Next() {
		var (
			row   ActivityRow
			desc  sql.NullString
			start sql.NullInt64
			end   sql.NullInt64
		)
		if err := rows.Scan(
			&row.ID, &row.SessionID, &row.CreatorID, &row.Title, &desc, &start, &end, &row.CreatedAtMs, &row.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if desc.Valid {
			row.Description = &desc.String
		}
		if start.Valid {
			row.StartAtMs = &start.Int64
		}
		if end.Valid {
			row.EndAtMs = &end.Int64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) ArchiveExpiredActivitySessions(ctx context.Context, nowMs int64) (int64, error) {
	if s == nil || s.db == nil {
		return 0, fmt.Errorf("db not initialized")
	}

	q := `UPDATE sessions
		SET status = ?, updated_at_ms = ?
		WHERE status = ?
		AND id IN (
			SELECT session_id FROM activities WHERE end_at_ms IS NOT NULL AND end_at_ms <= ?
		);`
	res, err := s.db.ExecContext(ctx, s.rebind(q), SessionStatusArchived, nowMs, SessionStatusActive, nowMs)
	if err != nil {
		return 0, err
	}
	affected, _ := res.RowsAffected()
	return affected, nil
}

func (s *Store) ArchiveActivitySessionIfExpired(ctx context.Context, activityID string, nowMs int64) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	if activityID == "" {
		return false, fmt.Errorf("missing activityID")
	}

	activity, err := s.GetActivityByID(ctx, activityID)
	if err != nil {
		return false, err
	}
	if activity.EndAtMs == nil || nowMs <= *activity.EndAtMs {
		return false, nil
	}

	q := `UPDATE sessions SET status = ?, updated_at_ms = ? WHERE id = ? AND status != ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), SessionStatusArchived, nowMs, activity.SessionID, SessionStatusArchived)
	if err != nil {
		return false, err
	}
	affected, _ := res.RowsAffected()
	return affected > 0, nil
}

func getOrCreateActivityInviteInTx(ctx context.Context, tx *sql.Tx, driver, activityID string, nowMs int64) (ActivityInviteRow, error) {
	const selectQ = `SELECT code, activity_id, created_at_ms, updated_at_ms FROM activity_invites WHERE activity_id = ?;`
	var existing ActivityInviteRow
	if err := tx.QueryRowContext(ctx, rebindQuery(driver, selectQ), activityID).Scan(&existing.Code, &existing.ActivityID, &existing.CreatedAtMs, &existing.UpdatedAtMs); err == nil {
		return existing, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ActivityInviteRow{}, err
	}

	for i := 0; i < 3; i++ {
		code, err := newInviteCode(8)
		if err != nil {
			return ActivityInviteRow{}, err
		}
		row := ActivityInviteRow{
			Code:        code,
			ActivityID:  activityID,
			CreatedAtMs: nowMs,
			UpdatedAtMs: nowMs,
		}
		const insertQ = `INSERT INTO activity_invites (code, activity_id, created_at_ms, updated_at_ms) VALUES (?, ?, ?, ?);`
		if _, err := tx.ExecContext(ctx, rebindQuery(driver, insertQ), row.Code, row.ActivityID, row.CreatedAtMs, row.UpdatedAtMs); err != nil {
			if isUniqueViolation(err) {
				continue
			}
			return ActivityInviteRow{}, err
		}
		return row, nil
	}
	return ActivityInviteRow{}, fmt.Errorf("failed to create invite code")
}

func getActivityByIDInTx(ctx context.Context, tx *sql.Tx, driver, activityID string) (ActivityRow, error) {
	q := rebindQuery(driver, `SELECT id, session_id, creator_id, title, description, start_at_ms, end_at_ms, created_at_ms, updated_at_ms
		FROM activities WHERE id = ?;`)
	var (
		row   ActivityRow
		desc  sql.NullString
		start sql.NullInt64
		end   sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, q, activityID).Scan(
		&row.ID, &row.SessionID, &row.CreatorID, &row.Title, &desc, &start, &end, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ActivityRow{}, fmt.Errorf("%w: activity", ErrNotFound)
		}
		return ActivityRow{}, err
	}
	if desc.Valid {
		row.Description = &desc.String
	}
	if start.Valid {
		row.StartAtMs = &start.Int64
	}
	if end.Valid {
		row.EndAtMs = &end.Int64
	}
	return row, nil
}

func getSessionByIDInTx(ctx context.Context, tx *sql.Tx, driver, sessionID string) (SessionRow, error) {
	q := rebindQuery(driver, `SELECT id, participants_hash, user1_id, user2_id, source, kind, status, last_message_text, last_message_at_ms, created_at_ms, updated_at_ms, hidden_by_users, reactivated_at_ms
		FROM sessions WHERE id = ?;`)
	var (
		session       SessionRow
		lastText      sql.NullString
		lastAtMs      sql.NullInt64
		hiddenBy      sql.NullString
		reactivatedAt sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, q, sessionID).Scan(
		&session.ID, &session.ParticipantsHash, &session.User1ID, &session.User2ID,
		&session.Source, &session.Kind, &session.Status, &lastText, &lastAtMs, &session.CreatedAtMs, &session.UpdatedAtMs,
		&hiddenBy, &reactivatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
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

func upsertSessionParticipantInTx(ctx context.Context, tx *sql.Tx, driver, sessionID, userID, role, status string, nowMs int64) (created bool, _ error) {
	role = strings.TrimSpace(role)
	status = strings.TrimSpace(status)
	if role == "" {
		role = SessionParticipantRoleMember
	}
	if status == "" {
		status = SessionParticipantStatusActive
	}

	selectQ := rebindQuery(driver, `SELECT status FROM session_participants WHERE session_id = ? AND user_id = ?;`)
	var existingStatus string
	if err := tx.QueryRowContext(ctx, selectQ, sessionID, userID).Scan(&existingStatus); err == nil {
		updateQ := rebindQuery(driver, `UPDATE session_participants
			SET role = ?, status = ?, updated_at_ms = ?
			WHERE session_id = ? AND user_id = ?;`)
		if _, err := tx.ExecContext(ctx, updateQ, role, status, nowMs, sessionID, userID); err != nil {
			return false, err
		}
		return false, nil
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	insertQ := rebindQuery(driver, `INSERT INTO session_participants (session_id, user_id, role, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?);`)
	if _, err := tx.ExecContext(ctx, insertQ, sessionID, userID, role, status, nowMs, nowMs); err != nil {
		return false, err
	}
	return true, nil
}

func normalizeOptionalText(v *string, maxLen int) *string {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	if maxLen > 0 && len(s) > maxLen {
		s = s[:maxLen]
	}
	return &s
}
