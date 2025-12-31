package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) AreFriends(ctx context.Context, userID, peerUserID string) (bool, error) {
	if s == nil || s.db == nil {
		return false, fmt.Errorf("db not initialized")
	}
	if userID == "" || peerUserID == "" {
		return false, fmt.Errorf("missing user ids")
	}

	q := `SELECT 1 FROM friends WHERE user_id = ? AND friend_id = ?;`
	var one int
	if err := s.db.QueryRowContext(ctx, s.rebind(q), userID, peerUserID).Scan(&one); err != nil {
		if err == sql.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return one == 1, nil
}

func (s *Store) ListFriends(ctx context.Context, userID string) ([]UserRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return nil, fmt.Errorf("missing userID")
	}

	q := `SELECT u.id, u.username, u.password_hash, u.display_name, u.avatar_url, u.created_at_ms, u.updated_at_ms
		FROM friends f
		JOIN users u ON u.id = f.friend_id
		WHERE f.user_id = ?
		ORDER BY u.display_name ASC, u.username ASC;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var friends []UserRow
	for rows.Next() {
		var u UserRow
		var avatar sql.NullString
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.DisplayName, &avatar, &u.CreatedAtMs, &u.UpdatedAtMs); err != nil {
			return nil, err
		}
		if avatar.Valid {
			u.AvatarURL = &avatar.String
		}
		friends = append(friends, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return friends, nil
}

func (s *Store) CreateFriendRequest(ctx context.Context, requesterID, addresseeID string, nowMs int64) (FriendRequestRow, bool, error) {
	if s == nil || s.db == nil {
		return FriendRequestRow{}, false, fmt.Errorf("db not initialized")
	}
	if requesterID == "" || addresseeID == "" {
		return FriendRequestRow{}, false, fmt.Errorf("missing user ids")
	}
	if requesterID == addresseeID {
		return FriendRequestRow{}, false, ErrCannotChatSelf
	}

	alreadyFriends, err := s.AreFriends(ctx, requesterID, addresseeID)
	if err != nil {
		return FriendRequestRow{}, false, err
	}
	if alreadyFriends {
		return FriendRequestRow{}, false, ErrAlreadyFriends
	}

	// If the reverse request exists and is pending, the correct action is to accept it instead.
	if reverse, err := s.getFriendRequestByPair(ctx, addresseeID, requesterID); err == nil {
		if reverse.Status == FriendRequestStatusPending {
			return FriendRequestRow{}, false, ErrRequestExists
		}
	}

	req := FriendRequestRow{
		ID:          uuid.NewString(),
		RequesterID: requesterID,
		AddresseeID: addresseeID,
		Status:      FriendRequestStatusPending,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	insertQ := `INSERT INTO friend_requests (id, requester_id, addressee_id, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?);`

	if _, err := s.db.ExecContext(ctx, s.rebind(insertQ),
		req.ID, req.RequesterID, req.AddresseeID, req.Status, req.CreatedAtMs, req.UpdatedAtMs,
	); err != nil {
		if !isUniqueViolation(err) {
			return FriendRequestRow{}, false, err
		}

		existing, err := s.getFriendRequestByPair(ctx, requesterID, addresseeID)
		if err != nil {
			return FriendRequestRow{}, false, err
		}
		switch existing.Status {
		case FriendRequestStatusPending:
			return FriendRequestRow{}, false, ErrRequestExists
		case FriendRequestStatusAccepted:
			return FriendRequestRow{}, false, ErrAlreadyFriends
		default:
			// Re-open the request (idempotent for the pair).
			updateQ := `UPDATE friend_requests SET status = ?, updated_at_ms = ? WHERE id = ?;`
			if _, err := s.db.ExecContext(ctx, s.rebind(updateQ), FriendRequestStatusPending, nowMs, existing.ID); err != nil {
				return FriendRequestRow{}, false, err
			}
			existing.Status = FriendRequestStatusPending
			existing.UpdatedAtMs = nowMs
			return existing, false, nil
		}
	}

	return req, true, nil
}

func (s *Store) ListFriendRequests(ctx context.Context, userID, box, status string) ([]FriendRequestRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return nil, fmt.Errorf("missing userID")
	}
	box = normalizeFriendBox(box)

	var q string
	var args []any

	switch box {
	case "incoming":
		q = `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
			FROM friend_requests WHERE addressee_id = ?`
		args = append(args, userID)
	default:
		q = `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
			FROM friend_requests WHERE requester_id = ?`
		args = append(args, userID)
	}

	if status != "" {
		q += " AND status = ?"
		args = append(args, status)
	}
	q += " ORDER BY updated_at_ms DESC LIMIT 50;"

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FriendRequestRow
	for rows.Next() {
		var r FriendRequestRow
		if err := rows.Scan(&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.CreatedAtMs, &r.UpdatedAtMs); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) AcceptFriendRequest(ctx context.Context, requestID, userID string, nowMs int64) (FriendRequestRow, error) {
	return s.mutateFriendRequest(ctx, requestID, userID, nowMs, "accept")
}

func (s *Store) RejectFriendRequest(ctx context.Context, requestID, userID string, nowMs int64) (FriendRequestRow, error) {
	return s.mutateFriendRequest(ctx, requestID, userID, nowMs, "reject")
}

func (s *Store) CancelFriendRequest(ctx context.Context, requestID, userID string, nowMs int64) (FriendRequestRow, error) {
	return s.mutateFriendRequest(ctx, requestID, userID, nowMs, "cancel")
}

func (s *Store) mutateFriendRequest(ctx context.Context, requestID, userID string, nowMs int64, action string) (FriendRequestRow, error) {
	if s == nil || s.db == nil {
		return FriendRequestRow{}, fmt.Errorf("db not initialized")
	}
	if requestID == "" || userID == "" {
		return FriendRequestRow{}, fmt.Errorf("missing ids")
	}

	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return FriendRequestRow{}, err
	}
	defer func() { _ = tx.Rollback() }()

	req, err := getFriendRequestByID(txCtx, tx, s.driver, requestID)
	if err != nil {
		return FriendRequestRow{}, err
	}

	switch action {
	case "accept":
		if req.AddresseeID != userID {
			return FriendRequestRow{}, ErrAccessDenied
		}
		if req.Status != FriendRequestStatusPending {
			return FriendRequestRow{}, ErrInvalidState
		}
		if err := setFriendRequestStatus(txCtx, tx, s.driver, req.ID, FriendRequestStatusAccepted, nowMs); err != nil {
			return FriendRequestRow{}, err
		}
		req.Status = FriendRequestStatusAccepted
		req.UpdatedAtMs = nowMs

		// Insert friendship both ways (idempotent).
		if err := upsertFriend(txCtx, tx, s.driver, req.RequesterID, req.AddresseeID, nowMs); err != nil {
			return FriendRequestRow{}, err
		}
		if err := upsertFriend(txCtx, tx, s.driver, req.AddresseeID, req.RequesterID, nowMs); err != nil {
			return FriendRequestRow{}, err
		}
	case "reject":
		if req.AddresseeID != userID {
			return FriendRequestRow{}, ErrAccessDenied
		}
		if req.Status != FriendRequestStatusPending {
			return FriendRequestRow{}, ErrInvalidState
		}
		if err := setFriendRequestStatus(txCtx, tx, s.driver, req.ID, FriendRequestStatusRejected, nowMs); err != nil {
			return FriendRequestRow{}, err
		}
		req.Status = FriendRequestStatusRejected
		req.UpdatedAtMs = nowMs
	case "cancel":
		if req.RequesterID != userID {
			return FriendRequestRow{}, ErrAccessDenied
		}
		if req.Status != FriendRequestStatusPending {
			return FriendRequestRow{}, ErrInvalidState
		}
		if err := setFriendRequestStatus(txCtx, tx, s.driver, req.ID, FriendRequestStatusCanceled, nowMs); err != nil {
			return FriendRequestRow{}, err
		}
		req.Status = FriendRequestStatusCanceled
		req.UpdatedAtMs = nowMs
	default:
		return FriendRequestRow{}, errors.New("unknown action")
	}

	if err := tx.Commit(); err != nil {
		return FriendRequestRow{}, err
	}
	return req, nil
}

func normalizeFriendBox(box string) string {
	switch box {
	case "incoming", "outgoing":
		return box
	default:
		return "incoming"
	}
}

func (s *Store) getFriendRequestByPair(ctx context.Context, requesterID, addresseeID string) (FriendRequestRow, error) {
	q := `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
		FROM friend_requests WHERE requester_id = ? AND addressee_id = ?;`
	var r FriendRequestRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), requesterID, addresseeID).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.CreatedAtMs, &r.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return FriendRequestRow{}, fmt.Errorf("%w: friend request", ErrNotFound)
		}
		return FriendRequestRow{}, err
	}
	return r, nil
}

func getFriendRequestByID(ctx context.Context, q sqlQueryer, driver, id string) (FriendRequestRow, error) {
	query := rebindQuery(driver, `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
		FROM friend_requests WHERE id = ?;`)
	var r FriendRequestRow
	if err := q.QueryRowContext(ctx, query, id).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.CreatedAtMs, &r.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return FriendRequestRow{}, fmt.Errorf("%w: friend request", ErrNotFound)
		}
		return FriendRequestRow{}, err
	}
	return r, nil
}

func setFriendRequestStatus(ctx context.Context, exec sqlExecer, driver, id, status string, nowMs int64) error {
	query := rebindQuery(driver, `UPDATE friend_requests SET status = ?, updated_at_ms = ? WHERE id = ?;`)
	res, err := exec.ExecContext(ctx, query, status, nowMs, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: friend request", ErrNotFound)
	}
	return nil
}

func upsertFriend(ctx context.Context, exec sqlExecer, driver, userID, friendID string, nowMs int64) error {
	query := rebindQuery(driver, `INSERT INTO friends (user_id, friend_id, created_at_ms)
		VALUES (?, ?, ?)
		ON CONFLICT(user_id, friend_id) DO NOTHING;`)
	_, err := exec.ExecContext(ctx, query, userID, friendID, nowMs)
	return err
}

type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
