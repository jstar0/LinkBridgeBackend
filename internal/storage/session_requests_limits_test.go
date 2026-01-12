package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestCreateSessionRequest_MapRateLimit10PerDay(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	requester, err := store.CreateUser(ctx, "req", "hash", "Requester", now)
	if err != nil {
		t.Fatalf("CreateUser(requester) error = %v", err)
	}

	for i := 0; i < 11; i++ {
		addressee, err := store.CreateUser(ctx, "u"+string(rune('a'+i)), "hash", "User", now)
		if err != nil {
			t.Fatalf("CreateUser(addressee %d) error = %v", i, err)
		}

		_, _, err = store.CreateSessionRequest(ctx, requester.ID, addressee.ID, SessionRequestSourceMap, nil, now)
		if i < 10 {
			if err != nil {
				t.Fatalf("CreateSessionRequest(%d) error = %v", i, err)
			}
		} else {
			if err != ErrRateLimited {
				t.Fatalf("CreateSessionRequest(11th) error = %v, want ErrRateLimited", err)
			}
		}
	}
}

func TestCreateSessionRequest_CooldownAfterReject(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	base := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	a, err := store.CreateUser(ctx, "a", "hash", "A", base)
	if err != nil {
		t.Fatalf("CreateUser(a) error = %v", err)
	}
	b, err := store.CreateUser(ctx, "b", "hash", "B", base)
	if err != nil {
		t.Fatalf("CreateUser(b) error = %v", err)
	}

	msg := "hi"
	req, _, err := store.CreateSessionRequest(ctx, a.ID, b.ID, SessionRequestSourceMap, &msg, base)
	if err != nil {
		t.Fatalf("CreateSessionRequest() error = %v", err)
	}

	rejectAt := base + 1000
	if _, err := store.RejectSessionRequest(ctx, req.ID, b.ID, rejectAt); err != nil {
		t.Fatalf("RejectSessionRequest() error = %v", err)
	}

	// Within 3 days -> blocked.
	if _, _, err := store.CreateSessionRequest(ctx, a.ID, b.ID, SessionRequestSourceMap, &msg, rejectAt+2*24*60*60*1000); err != ErrCooldownActive {
		t.Fatalf("CreateSessionRequest(within cooldown) error = %v, want ErrCooldownActive", err)
	}

	// After 3 days -> allowed (re-open).
	_, created, err := store.CreateSessionRequest(ctx, a.ID, b.ID, SessionRequestSourceMap, &msg, rejectAt+3*24*60*60*1000+1)
	if err != nil {
		t.Fatalf("CreateSessionRequest(after cooldown) error = %v", err)
	}
	if created {
		t.Fatalf("CreateSessionRequest(after cooldown) created = true, want false (re-open)")
	}
}
