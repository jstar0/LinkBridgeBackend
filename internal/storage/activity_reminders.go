package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) UpsertActivityReminder(ctx context.Context, activityID, userID string, remindAtMs, nowMs int64) (ActivityReminderRow, error) {
	if s == nil || s.db == nil {
		return ActivityReminderRow{}, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	userID = strings.TrimSpace(userID)
	if activityID == "" || userID == "" {
		return ActivityReminderRow{}, fmt.Errorf("missing required fields")
	}
	if remindAtMs <= 0 {
		return ActivityReminderRow{}, fmt.Errorf("invalid remindAtMs")
	}

	q := `INSERT INTO activity_reminders (
			activity_id, user_id, remind_at_ms, status, last_error, sent_at_ms, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(activity_id, user_id) DO UPDATE SET
			remind_at_ms = excluded.remind_at_ms,
			status = excluded.status,
			last_error = excluded.last_error,
			sent_at_ms = excluded.sent_at_ms,
			updated_at_ms = excluded.updated_at_ms;`

	if _, err := s.db.ExecContext(ctx, s.rebind(q), activityID, userID, remindAtMs, ActivityReminderStatusPending, nil, nil, nowMs, nowMs); err != nil {
		return ActivityReminderRow{}, err
	}

	return s.GetActivityReminder(ctx, activityID, userID)
}

func (s *Store) GetActivityReminder(ctx context.Context, activityID, userID string) (ActivityReminderRow, error) {
	if s == nil || s.db == nil {
		return ActivityReminderRow{}, fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	userID = strings.TrimSpace(userID)
	if activityID == "" || userID == "" {
		return ActivityReminderRow{}, fmt.Errorf("missing required fields")
	}

	q := `SELECT activity_id, user_id, remind_at_ms, status, last_error, sent_at_ms, created_at_ms, updated_at_ms
		FROM activity_reminders
		WHERE activity_id = ? AND user_id = ?;`

	var row ActivityReminderRow
	var lastErr sql.NullString
	var sentAt sql.NullInt64
	if err := s.db.QueryRowContext(ctx, s.rebind(q), activityID, userID).Scan(
		&row.ActivityID,
		&row.UserID,
		&row.RemindAtMs,
		&row.Status,
		&lastErr,
		&sentAt,
		&row.CreatedAtMs,
		&row.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return ActivityReminderRow{}, fmt.Errorf("%w: activity reminder", ErrNotFound)
		}
		return ActivityReminderRow{}, err
	}
	if lastErr.Valid {
		row.LastError = &lastErr.String
	}
	if sentAt.Valid {
		row.SentAtMs = &sentAt.Int64
	}
	return row, nil
}

func (s *Store) ListDueActivityReminders(ctx context.Context, nowMs int64, limit int) ([]ActivityReminderRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	q := `SELECT activity_id, user_id, remind_at_ms, status, last_error, sent_at_ms, created_at_ms, updated_at_ms
		FROM activity_reminders
		WHERE status = ? AND remind_at_ms <= ?
		ORDER BY remind_at_ms ASC
		LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), ActivityReminderStatusPending, nowMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]ActivityReminderRow, 0, 8)
	for rows.Next() {
		var row ActivityReminderRow
		var lastErr sql.NullString
		var sentAt sql.NullInt64
		if err := rows.Scan(
			&row.ActivityID,
			&row.UserID,
			&row.RemindAtMs,
			&row.Status,
			&lastErr,
			&sentAt,
			&row.CreatedAtMs,
			&row.UpdatedAtMs,
		); err != nil {
			return nil, err
		}
		if lastErr.Valid {
			row.LastError = &lastErr.String
		}
		if sentAt.Valid {
			row.SentAtMs = &sentAt.Int64
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) MarkActivityReminderSent(ctx context.Context, activityID, userID string, nowMs int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	userID = strings.TrimSpace(userID)
	if activityID == "" || userID == "" {
		return fmt.Errorf("missing required fields")
	}

	q := `UPDATE activity_reminders
		SET status = ?, sent_at_ms = ?, updated_at_ms = ?
		WHERE activity_id = ? AND user_id = ?;`
	_, err := s.db.ExecContext(ctx, s.rebind(q), ActivityReminderStatusSent, nowMs, nowMs, activityID, userID)
	return err
}

func (s *Store) MarkActivityReminderFailed(ctx context.Context, activityID, userID, errMsg string, nowMs int64) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	activityID = strings.TrimSpace(activityID)
	userID = strings.TrimSpace(userID)
	errMsg = strings.TrimSpace(errMsg)
	if activityID == "" || userID == "" {
		return fmt.Errorf("missing required fields")
	}
	if errMsg == "" {
		errMsg = "send failed"
	}
	if len(errMsg) > 400 {
		errMsg = errMsg[:400]
	}

	q := `UPDATE activity_reminders
		SET status = ?, last_error = ?, updated_at_ms = ?
		WHERE activity_id = ? AND user_id = ?;`
	_, err := s.db.ExecContext(ctx, s.rebind(q), ActivityReminderStatusFailed, errMsg, nowMs, activityID, userID)
	return err
}
