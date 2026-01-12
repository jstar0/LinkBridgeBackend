package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

func (s *Store) GetOrCreateSessionInvite(ctx context.Context, inviterID string, nowMs int64) (SessionInviteRow, bool, error) {
	if s == nil || s.db == nil {
		return SessionInviteRow{}, false, fmt.Errorf("db not initialized")
	}
	inviterID = strings.TrimSpace(inviterID)
	if inviterID == "" {
		return SessionInviteRow{}, false, fmt.Errorf("missing inviterID")
	}

	// Reuse an existing code to keep the WeChat code stable.
	const selectQ = `SELECT
			code,
			inviter_id,
			expires_at_ms,
			geo_fence_lat_e7,
			geo_fence_lng_e7,
			geo_fence_radius_m,
			created_at_ms,
			updated_at_ms
		FROM session_invites WHERE inviter_id = ?;`
	var existing SessionInviteRow
	var (
		expires sql.NullInt64
		gfLat   sql.NullInt64
		gfLng   sql.NullInt64
		gfRad   sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(selectQ), inviterID).Scan(
		&existing.Code, &existing.InviterID, &expires, &gfLat, &gfLng, &gfRad, &existing.CreatedAtMs, &existing.UpdatedAtMs,
	); err == nil {
		if expires.Valid && expires.Int64 > 0 {
			existing.ExpiresAtMs = &expires.Int64
		}
		if gfLat.Valid && gfLng.Valid && gfRad.Valid && gfRad.Int64 > 0 {
			existing.GeoFence = &GeoFence{
				LatE7:   gfLat.Int64,
				LngE7:   gfLng.Int64,
				RadiusM: int(gfRad.Int64),
			}
		}
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

		const insertQ = `INSERT INTO session_invites (
				code, inviter_id, expires_at_ms, geo_fence_lat_e7, geo_fence_lng_e7, geo_fence_radius_m, created_at_ms, updated_at_ms
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?);`
		if _, err := s.db.ExecContext(ctx, s.rebind(insertQ),
			row.Code, row.InviterID, nil, nil, nil, nil, row.CreatedAtMs, row.UpdatedAtMs,
		); err != nil {
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
	code = strings.TrimSpace(code)
	if code == "" {
		return SessionInviteRow{}, fmt.Errorf("missing code")
	}

	const q = `SELECT
			code,
			inviter_id,
			expires_at_ms,
			geo_fence_lat_e7,
			geo_fence_lng_e7,
			geo_fence_radius_m,
			created_at_ms,
			updated_at_ms
		FROM session_invites WHERE code = ?;`
	var row SessionInviteRow
	var (
		expires sql.NullInt64
		gfLat   sql.NullInt64
		gfLng   sql.NullInt64
		gfRad   sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, s.rebind(q), code).Scan(
		&row.Code, &row.InviterID, &expires, &gfLat, &gfLng, &gfRad, &row.CreatedAtMs, &row.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionInviteRow{}, ErrInviteInvalid
		}
		return SessionInviteRow{}, err
	}
	if expires.Valid && expires.Int64 > 0 {
		row.ExpiresAtMs = &expires.Int64
	}
	if gfLat.Valid && gfLng.Valid && gfRad.Valid && gfRad.Int64 > 0 {
		row.GeoFence = &GeoFence{LatE7: gfLat.Int64, LngE7: gfLng.Int64, RadiusM: int(gfRad.Int64)}
	}
	return row, nil
}

func (s *Store) UpdateSessionInviteSettings(ctx context.Context, inviterID string, expiresAtMs *int64, geoFence *GeoFence, nowMs int64) (SessionInviteRow, error) {
	if s == nil || s.db == nil {
		return SessionInviteRow{}, fmt.Errorf("db not initialized")
	}
	inviterID = strings.TrimSpace(inviterID)
	if inviterID == "" {
		return SessionInviteRow{}, fmt.Errorf("missing inviterID")
	}

	// Ensure invite exists (stable code).
	if _, _, err := s.GetOrCreateSessionInvite(ctx, inviterID, nowMs); err != nil {
		return SessionInviteRow{}, err
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

	q := `UPDATE session_invites
		SET expires_at_ms = ?, geo_fence_lat_e7 = ?, geo_fence_lng_e7 = ?, geo_fence_radius_m = ?, updated_at_ms = ?
		WHERE inviter_id = ?;`
	if _, err := s.db.ExecContext(ctx, s.rebind(q), exp, lat, lng, rad, nowMs, inviterID); err != nil {
		return SessionInviteRow{}, err
	}

	row, _, err := s.GetOrCreateSessionInvite(ctx, inviterID, nowMs)
	return row, err
}

func (s *Store) ConsumeSessionInvite(ctx context.Context, code string, atLatE7, atLngE7 *int64, nowMs int64) (SessionInviteRow, error) {
	row, err := s.ResolveSessionInvite(ctx, code)
	if err != nil {
		return SessionInviteRow{}, err
	}

	if row.ExpiresAtMs != nil && nowMs > *row.ExpiresAtMs {
		return SessionInviteRow{}, ErrInviteExpired
	}

	if row.GeoFence != nil && row.GeoFence.RadiusM > 0 {
		if atLatE7 == nil || atLngE7 == nil {
			return SessionInviteRow{}, ErrGeoFenceRequired
		}
		dist := distanceMetersE7(row.GeoFence.LatE7, row.GeoFence.LngE7, *atLatE7, *atLngE7)
		if dist > float64(row.GeoFence.RadiusM) {
			return SessionInviteRow{}, ErrGeoFenceForbidden
		}
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
