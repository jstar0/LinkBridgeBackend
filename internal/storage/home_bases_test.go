package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestUpsertHomeBase_DailyLimit(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, 1, 11, 12, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	user, err := store.CreateUser(ctx, "u1", "hash", "User1", now)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	if _, err := store.UpsertHomeBase(ctx, user.ID, 310000000, 1210000000, nil, now); err != nil {
		t.Fatalf("UpsertHomeBase(first) error = %v", err)
	}

	// Same day, up to 3 location changes are allowed.
	if _, err := store.UpsertHomeBase(ctx, user.ID, 320000000, 1220000000, nil, now+1000); err != nil {
		t.Fatalf("UpsertHomeBase(second) error = %v", err)
	}
	if _, err := store.UpsertHomeBase(ctx, user.ID, 330000000, 1230000000, nil, now+2000); err != nil {
		t.Fatalf("UpsertHomeBase(third) error = %v", err)
	}

	// Same day, 4th different coordinates -> blocked.
	if _, err := store.UpsertHomeBase(ctx, user.ID, 340000000, 1240000000, nil, now+3000); err != ErrHomeBaseLimited {
		t.Fatalf("UpsertHomeBase(fourth) error = %v, want ErrHomeBaseLimited", err)
	}

	// Same day, same coordinates -> idempotent OK.
	if _, err := store.UpsertHomeBase(ctx, user.ID, 310000000, 1210000000, nil, now+4000); err != nil {
		t.Fatalf("UpsertHomeBase(idempotent) error = %v", err)
	}

	// Radius-only change should not consume the location update quota.
	r := 2000
	if _, err := store.UpsertHomeBase(ctx, user.ID, 310000000, 1210000000, &r, now+5000); err != nil {
		t.Fatalf("UpsertHomeBase(radius-only) error = %v", err)
	}

	// Next day -> allowed.
	if _, err := store.UpsertHomeBase(ctx, user.ID, 320000000, 1220000000, nil, now+24*60*60*1000+1000); err != nil {
		t.Fatalf("UpsertHomeBase(next day) error = %v", err)
	}
}
