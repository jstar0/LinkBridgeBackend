package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateSessionRequest(ctx context.Context, requesterID, addresseeID string, nowMs int64) (SessionRequestRow, bool, error) {
	if s == nil || s.db == nil {
		return SessionRequestRow{}, false, fmt.Errorf("db not initialized")
	}
	if requesterID == "" || addresseeID == "" {
		return SessionRequestRow{}, false, fmt.Errorf("missing user ids")
	}
	if requesterID == addresseeID {
		return SessionRequestRow{}, false, ErrCannotChatSelf
	}

	// Check if there's already an active session between these users
	existingSession, err := s.getSessionByParticipants(ctx, requesterID, addresseeID)
	if err == nil && existingSession.Status == SessionStatusActive {
		return SessionRequestRow{}, false, ErrSessionExists
	}

	// If the reverse request exists and is pending, return error
	if reverse, err := s.getSessionRequestByPair(ctx, addresseeID, requesterID); err == nil {
		if reverse.Status == SessionRequestStatusPending {
			return SessionRequestRow{}, false, ErrRequestExists
		}
	}

	req := SessionRequestRow{
		ID:          uuid.NewString(),
		RequesterID: requesterID,
		AddresseeID: addresseeID,
		Status:      SessionRequestStatusPending,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	insertQ := `INSERT INTO session_requests (id, requester_id, addressee_id, status, created_at_ms, updated_at_ms)
		VALUES (?, ?, ?, ?, ?, ?);`

	if _, err := s.db.ExecContext(ctx, s.rebind(insertQ),
		req.ID, req.RequesterID, req.AddresseeID, req.Status, req.CreatedAtMs, req.UpdatedAtMs,
	); err != nil {
		if !isUniqueViolation(err) {
			return SessionRequestRow{}, false, err
		}

		existing, err := s.getSessionRequestByPair(ctx, requesterID, addresseeID)
		if err != nil {
			return SessionRequestRow{}, false, err
		}
		switch existing.Status {
		case SessionRequestStatusPending:
			return SessionRequestRow{}, false, ErrRequestExists
		case SessionRequestStatusAccepted:
			return SessionRequestRow{}, false, ErrSessionExists
		default:
			// Re-open the request
			updateQ := `UPDATE session_requests SET status = ?, updated_at_ms = ? WHERE id = ?;`
			if _, err := s.db.ExecContext(ctx, s.rebind(updateQ), SessionRequestStatusPending, nowMs, existing.ID); err != nil {
				return SessionRequestRow{}, false, err
			}
			existing.Status = SessionRequestStatusPending
			existing.UpdatedAtMs = nowMs
			return existing, false, nil
		}
	}

	return req, true, nil
}

func (s *Store) ListSessionRequests(ctx context.Context, userID, box, status string) ([]SessionRequestRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return nil, fmt.Errorf("missing userID")
	}
	box = normalizeBox(box)

	var q string
	var args []any

	switch box {
	case "incoming":
		q = `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
			FROM session_requests WHERE addressee_id = ?`
		args = append(args, userID)
	default:
		q = `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
			FROM session_requests WHERE requester_id = ?`
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

	var out []SessionRequestRow
	for rows.Next() {
		var r SessionRequestRow
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

func (s *Store) AcceptSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (SessionRequestRow, *SessionRow, error) {
	return s.mutateSessionRequest(ctx, requestID, userID, nowMs, "accept")
}

func (s *Store) RejectSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (SessionRequestRow, error) {
	req, _, err := s.mutateSessionRequest(ctx, requestID, userID, nowMs, "reject")
	return req, err
}

func (s *Store) CancelSessionRequest(ctx context.Context, requestID, userID string, nowMs int64) (SessionRequestRow, error) {
	req, _, err := s.mutateSessionRequest(ctx, requestID, userID, nowMs, "cancel")
	return req, err
}

func (s *Store) mutateSessionRequest(ctx context.Context, requestID, userID string, nowMs int64, action string) (SessionRequestRow, *SessionRow, error) {
	if s == nil || s.db == nil {
		return SessionRequestRow{}, nil, fmt.Errorf("db not initialized")
	}
	if requestID == "" || userID == "" {
		return SessionRequestRow{}, nil, fmt.Errorf("missing ids")
	}

	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return SessionRequestRow{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	req, err := getSessionRequestByID(txCtx, tx, s.driver, requestID)
	if err != nil {
		return SessionRequestRow{}, nil, err
	}

	var session *SessionRow

	switch action {
	case "accept":
		if req.AddresseeID != userID {
			return SessionRequestRow{}, nil, ErrAccessDenied
		}
		if req.Status != SessionRequestStatusPending {
			return SessionRequestRow{}, nil, ErrInvalidState
		}
		if err := setSessionRequestStatus(txCtx, tx, s.driver, req.ID, SessionRequestStatusAccepted, nowMs); err != nil {
			return SessionRequestRow{}, nil, err
		}
		req.Status = SessionRequestStatusAccepted
		req.UpdatedAtMs = nowMs

		// Create session between the two users
		sess, err := createSessionInTx(txCtx, tx, s.driver, req.RequesterID, req.AddresseeID, nowMs)
		if err != nil && !errors.Is(err, ErrSessionExists) {
			return SessionRequestRow{}, nil, err
		}
		session = &sess
	case "reject":
		if req.AddresseeID != userID {
			return SessionRequestRow{}, nil, ErrAccessDenied
		}
		if req.Status != SessionRequestStatusPending {
			return SessionRequestRow{}, nil, ErrInvalidState
		}
		if err := setSessionRequestStatus(txCtx, tx, s.driver, req.ID, SessionRequestStatusRejected, nowMs); err != nil {
			return SessionRequestRow{}, nil, err
		}
		req.Status = SessionRequestStatusRejected
		req.UpdatedAtMs = nowMs
	case "cancel":
		if req.RequesterID != userID {
			return SessionRequestRow{}, nil, ErrAccessDenied
		}
		if req.Status != SessionRequestStatusPending {
			return SessionRequestRow{}, nil, ErrInvalidState
		}
		if err := setSessionRequestStatus(txCtx, tx, s.driver, req.ID, SessionRequestStatusCanceled, nowMs); err != nil {
			return SessionRequestRow{}, nil, err
		}
		req.Status = SessionRequestStatusCanceled
		req.UpdatedAtMs = nowMs
	default:
		return SessionRequestRow{}, nil, errors.New("unknown action")
	}

	if err := tx.Commit(); err != nil {
		return SessionRequestRow{}, nil, err
	}
	return req, session, nil
}

func normalizeBox(box string) string {
	switch box {
	case "incoming", "outgoing":
		return box
	default:
		return "incoming"
	}
}

func (s *Store) getSessionRequestByPair(ctx context.Context, requesterID, addresseeID string) (SessionRequestRow, error) {
	q := `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
		FROM session_requests WHERE requester_id = ? AND addressee_id = ?;`
	var r SessionRequestRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), requesterID, addresseeID).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.CreatedAtMs, &r.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRequestRow{}, fmt.Errorf("%w: session request", ErrNotFound)
		}
		return SessionRequestRow{}, err
	}
	return r, nil
}

func getSessionRequestByID(ctx context.Context, q sqlQueryer, driver, id string) (SessionRequestRow, error) {
	query := rebindQuery(driver, `SELECT id, requester_id, addressee_id, status, created_at_ms, updated_at_ms
		FROM session_requests WHERE id = ?;`)
	var r SessionRequestRow
	if err := q.QueryRowContext(ctx, query, id).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.CreatedAtMs, &r.UpdatedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRequestRow{}, fmt.Errorf("%w: session request", ErrNotFound)
		}
		return SessionRequestRow{}, err
	}
	return r, nil
}

func setSessionRequestStatus(ctx context.Context, exec sqlExecer, driver, id, status string, nowMs int64) error {
	query := rebindQuery(driver, `UPDATE session_requests SET status = ?, updated_at_ms = ? WHERE id = ?;`)
	res, err := exec.ExecContext(ctx, query, status, nowMs, id)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: session request", ErrNotFound)
	}
	return nil
}

type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
