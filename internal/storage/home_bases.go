package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func (s *Store) GetHomeBase(ctx context.Context, userID string) (HomeBaseRow, error) {
	if s == nil || s.db == nil {
		return HomeBaseRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return HomeBaseRow{}, fmt.Errorf("missing userID")
	}

	q := `SELECT user_id, lat_e7, lng_e7, last_updated_ymd, created_at_ms, updated_at_ms
		FROM home_bases WHERE user_id = ?;`
	var hb HomeBaseRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID).Scan(
		&hb.UserID, &hb.LatE7, &hb.LngE7, &hb.LastUpdatedYMD, &hb.CreatedAtMs, &hb.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return HomeBaseRow{}, fmt.Errorf("%w: home base", ErrNotFound)
		}
		return HomeBaseRow{}, err
	}
	return hb, nil
}

func (s *Store) UpsertHomeBase(ctx context.Context, userID string, latE7, lngE7 int64, nowMs int64) (HomeBaseRow, error) {
	if s == nil || s.db == nil {
		return HomeBaseRow{}, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return HomeBaseRow{}, fmt.Errorf("missing userID")
	}

	todayYMD := ymdInResetTZ(nowMs)

	existing, err := s.GetHomeBase(ctx, userID)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return HomeBaseRow{}, err
	}

	if err == nil {
		if existing.LastUpdatedYMD == todayYMD {
			if existing.LatE7 == latE7 && existing.LngE7 == lngE7 {
				return existing, nil
			}
			return HomeBaseRow{}, ErrHomeBaseLimited
		}

		updateQ := `UPDATE home_bases
			SET lat_e7 = ?, lng_e7 = ?, last_updated_ymd = ?, updated_at_ms = ?
			WHERE user_id = ?;`
		if _, err := s.db.ExecContext(ctx, s.rebind(updateQ), latE7, lngE7, todayYMD, nowMs, userID); err != nil {
			return HomeBaseRow{}, err
		}
		existing.LatE7 = latE7
		existing.LngE7 = lngE7
		existing.LastUpdatedYMD = todayYMD
		existing.UpdatedAtMs = nowMs
		return existing, nil
	}

	hb := HomeBaseRow{
		UserID:         userID,
		LatE7:          latE7,
		LngE7:          lngE7,
		LastUpdatedYMD: todayYMD,
		CreatedAtMs:    nowMs,
		UpdatedAtMs:    nowMs,
	}

	insertQ := `INSERT INTO home_bases (user_id, lat_e7, lng_e7, last_updated_ymd, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?);`
	if _, err := s.db.ExecContext(ctx, s.rebind(insertQ), hb.UserID, hb.LatE7, hb.LngE7, hb.LastUpdatedYMD, hb.CreatedAtMs, hb.UpdatedAtMs); err != nil {
		if isUniqueViolation(err) {
			// Race: try update path.
			return s.UpsertHomeBase(ctx, userID, latE7, lngE7, nowMs)
		}
		return HomeBaseRow{}, err
	}
	return hb, nil
}
