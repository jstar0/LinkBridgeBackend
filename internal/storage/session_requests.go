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

func (s *Store) CreateSessionRequest(ctx context.Context, requesterID, addresseeID, source string, verificationMessage *string, nowMs int64) (SessionRequestRow, bool, error) {
	if s == nil || s.db == nil {
		return SessionRequestRow{}, false, fmt.Errorf("db not initialized")
	}
	if requesterID == "" || addresseeID == "" {
		return SessionRequestRow{}, false, fmt.Errorf("missing user ids")
	}
	if requesterID == addresseeID {
		return SessionRequestRow{}, false, ErrCannotChatSelf
	}

	source = normalizeSessionRequestSource(source)

	// Rate limit only applies to map-based relationship requests.
	if source == SessionRequestSourceMap {
		dayStartMs, dayEndMs := dayBoundsMsInResetTZ(nowMs)
		countQ := `SELECT COUNT(*) FROM session_requests
			WHERE requester_id = ? AND source = ? AND last_opened_at_ms >= ? AND last_opened_at_ms < ?;`
		var n int
		if err := s.db.QueryRowContext(ctx, s.rebind(countQ), requesterID, source, dayStartMs, dayEndMs).Scan(&n); err != nil {
			return SessionRequestRow{}, false, err
		}
		if n >= 10 {
			return SessionRequestRow{}, false, ErrRateLimited
		}
	}

	// Check if there's already an active session between these users
	existingSession, err := s.getSessionByParticipants(ctx, requesterID, addresseeID)
	if err == nil && existingSession.Status == SessionStatusActive {
		return SessionRequestRow{}, false, ErrSessionExists
	}
	// 如果会话是归档状态，允许创建请求，接受时会激活会话

	// If the reverse request exists and is pending, return error
	if reverse, err := s.getSessionRequestByPair(ctx, addresseeID, requesterID); err == nil {
		if reverse.Status == SessionRequestStatusPending {
			return SessionRequestRow{}, false, ErrRequestExists
		}
	}

	req := SessionRequestRow{
		ID:                  uuid.NewString(),
		RequesterID:         requesterID,
		AddresseeID:         addresseeID,
		Status:              SessionRequestStatusPending,
		Source:              source,
		VerificationMessage: verificationMessage,
		CreatedAtMs:         nowMs,
		UpdatedAtMs:         nowMs,
		LastOpenedAtMs:      nowMs,
	}

	insertQ := `INSERT INTO session_requests (
			id, requester_id, addressee_id, status, source, verification_message, created_at_ms, updated_at_ms, last_opened_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);`

	if _, err := s.db.ExecContext(ctx, s.rebind(insertQ),
		req.ID, req.RequesterID, req.AddresseeID, req.Status, req.Source, req.VerificationMessage, req.CreatedAtMs, req.UpdatedAtMs, req.LastOpenedAtMs,
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
			if existing.Status == SessionRequestStatusRejected && nowMs-existing.UpdatedAtMs < 3*24*60*60*1000 {
				return SessionRequestRow{}, false, ErrCooldownActive
			}

			// Re-open the request
			updateQ := `UPDATE session_requests
				SET status = ?, source = ?, verification_message = ?, updated_at_ms = ?, last_opened_at_ms = ?
				WHERE id = ?;`
			if _, err := s.db.ExecContext(ctx, s.rebind(updateQ), SessionRequestStatusPending, source, verificationMessage, nowMs, nowMs, existing.ID); err != nil {
				return SessionRequestRow{}, false, err
			}
			existing.Status = SessionRequestStatusPending
			existing.Source = source
			existing.VerificationMessage = verificationMessage
			existing.UpdatedAtMs = nowMs
			existing.LastOpenedAtMs = nowMs
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
		q = `SELECT id, requester_id, addressee_id, status, source, verification_message, created_at_ms, updated_at_ms, last_opened_at_ms
			FROM session_requests WHERE addressee_id = ?`
		args = append(args, userID)
	default:
		q = `SELECT id, requester_id, addressee_id, status, source, verification_message, created_at_ms, updated_at_ms, last_opened_at_ms
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
		if err := rows.Scan(&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.Source, &r.VerificationMessage, &r.CreatedAtMs, &r.UpdatedAtMs, &r.LastOpenedAtMs); err != nil {
			return nil, err
		}
		if r.Source == "" {
			r.Source = SessionRequestSourceWeChatCode
		}
		if r.LastOpenedAtMs == 0 {
			r.LastOpenedAtMs = r.CreatedAtMs
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

		// Create session between the two users, or reactivate if archived
		sess, err := createSessionInTx(txCtx, tx, s.driver, req.RequesterID, req.AddresseeID, req.Source, nowMs)
		if err != nil {
			if errors.Is(err, ErrSessionExists) {
				// 会话已存在，检查是否需要激活
				if sess.Status == SessionStatusArchived {
					// 激活归档的会话
					updateQ := rebindQuery(s.driver, `UPDATE sessions SET status = ?, source = ?, reactivated_at_ms = ?, updated_at_ms = ? WHERE id = ?;`)
					if _, err := tx.ExecContext(txCtx, updateQ, SessionStatusActive, normalizeSessionSource(req.Source), nowMs, nowMs, sess.ID); err != nil {
						return SessionRequestRow{}, nil, err
					}
					sess.Status = SessionStatusActive
					sess.Source = normalizeSessionSource(req.Source)
					sess.ReactivatedAtMs = &nowMs
					sess.UpdatedAtMs = nowMs
				}
				session = &sess
			} else {
				return SessionRequestRow{}, nil, err
			}
		} else {
			session = &sess
		}

		// Default grouping for map-based long-lived relationships:
		// For both sides, ensure a default group exists and assign the relationship into it
		// (only if the user hasn't customized meta yet).
		if normalizeSessionSource(req.Source) == SessionSourceMap && session != nil {
			const defaultMapGroupName = "地图"

			requesterGroup, err := getOrCreateRelationshipGroupByNameInTx(txCtx, tx, s.driver, req.RequesterID, defaultMapGroupName, nowMs)
			if err != nil {
				return SessionRequestRow{}, nil, err
			}
			addresseeGroup, err := getOrCreateRelationshipGroupByNameInTx(txCtx, tx, s.driver, req.AddresseeID, defaultMapGroupName, nowMs)
			if err != nil {
				return SessionRequestRow{}, nil, err
			}
			if err := insertDefaultSessionUserMetaIfMissing(txCtx, tx, s.driver, session.ID, req.RequesterID, &requesterGroup.ID, nowMs); err != nil {
				return SessionRequestRow{}, nil, err
			}
			if err := insertDefaultSessionUserMetaIfMissing(txCtx, tx, s.driver, session.ID, req.AddresseeID, &addresseeGroup.ID, nowMs); err != nil {
				return SessionRequestRow{}, nil, err
			}
		}
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
	q := `SELECT id, requester_id, addressee_id, status, source, verification_message, created_at_ms, updated_at_ms, last_opened_at_ms
		FROM session_requests WHERE requester_id = ? AND addressee_id = ?;`
	var r SessionRequestRow
	if err := s.db.QueryRowContext(ctx, s.rebind(q), requesterID, addresseeID).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.Source, &r.VerificationMessage, &r.CreatedAtMs, &r.UpdatedAtMs, &r.LastOpenedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRequestRow{}, fmt.Errorf("%w: session request", ErrNotFound)
		}
		return SessionRequestRow{}, err
	}
	if r.Source == "" {
		r.Source = SessionRequestSourceWeChatCode
	}
	if r.LastOpenedAtMs == 0 {
		r.LastOpenedAtMs = r.CreatedAtMs
	}
	return r, nil
}

func getSessionRequestByID(ctx context.Context, q sqlQueryer, driver, id string) (SessionRequestRow, error) {
	query := rebindQuery(driver, `SELECT id, requester_id, addressee_id, status, source, verification_message, created_at_ms, updated_at_ms, last_opened_at_ms
		FROM session_requests WHERE id = ?;`)
	var r SessionRequestRow
	if err := q.QueryRowContext(ctx, query, id).Scan(
		&r.ID, &r.RequesterID, &r.AddresseeID, &r.Status, &r.Source, &r.VerificationMessage, &r.CreatedAtMs, &r.UpdatedAtMs, &r.LastOpenedAtMs,
	); err != nil {
		if err == sql.ErrNoRows {
			return SessionRequestRow{}, fmt.Errorf("%w: session request", ErrNotFound)
		}
		return SessionRequestRow{}, err
	}
	if r.Source == "" {
		r.Source = SessionRequestSourceWeChatCode
	}
	if r.LastOpenedAtMs == 0 {
		r.LastOpenedAtMs = r.CreatedAtMs
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

func normalizeSessionRequestSource(source string) string {
	source = strings.TrimSpace(source)
	switch source {
	case SessionRequestSourceMap:
		return SessionRequestSourceMap
	case SessionRequestSourceWeChatCode:
		return SessionRequestSourceWeChatCode
	case "":
		return SessionRequestSourceWeChatCode
	default:
		return source
	}
}

type sqlQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type sqlExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}
