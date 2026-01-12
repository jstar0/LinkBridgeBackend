package storage

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func TestLocalFeed_PostVisibilityByRadius(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	u, err := store.CreateUser(ctx, "source", "hash", "SourceUser", now)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	// Home base at (31.0, 121.0).
	radius := 1000
	if _, err := store.UpsertHomeBase(ctx, u.ID, 310000000, 1210000000, &radius, now); err != nil {
		t.Fatalf("UpsertHomeBase() error = %v", err)
	}

	text := "hello"
	expiresAt := now + 24*60*60*1000
	if _, _, err := store.CreateLocalFeedPost(ctx, u.ID, &text, nil, expiresAt, false, now); err != nil {
		t.Fatalf("CreateLocalFeedPost() error = %v", err)
	}

	nearLat := int64(310000000)
	nearLng := int64(1210000000)
	near, err := store.ListLocalFeedPostsForSource(ctx, u.ID, &nearLat, &nearLng, now, 50)
	if err != nil {
		t.Fatalf("ListLocalFeedPostsForSource(near) error = %v", err)
	}
	if len(near) != 1 {
		t.Fatalf("near posts = %d, want 1", len(near))
	}

	farLat := int64(0)
	farLng := int64(0)
	far, err := store.ListLocalFeedPostsForSource(ctx, u.ID, &farLat, &farLng, now, 50)
	if err != nil {
		t.Fatalf("ListLocalFeedPostsForSource(far) error = %v", err)
	}
	if len(far) != 0 {
		t.Fatalf("far posts = %d, want 0", len(far))
	}
}

func TestLocalFeed_PinsUseMapProfileOverride(t *testing.T) {
	ctx := context.Background()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	store, err := Open(ctx, "sqlite::memory:", logger)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = store.Close() }()

	now := time.Date(2026, 1, 11, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60)).UnixMilli()

	u, err := store.CreateUser(ctx, "pinuser", "hash", "CoreName", now)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if _, err := store.UpsertHomeBase(ctx, u.ID, 310000000, 1210000000, nil, now); err != nil {
		t.Fatalf("UpsertHomeBase() error = %v", err)
	}

	override := "MapNick"
	if _, err := store.UpsertUserMapProfile(ctx, u.ID, &override, nil, "{}", now); err != nil {
		t.Fatalf("UpsertUserMapProfile() error = %v", err)
	}

	pins, err := store.ListLocalFeedPins(ctx, 300000000, 320000000, 1200000000, 1220000000, 310000000, 1210000000, 10)
	if err != nil {
		t.Fatalf("ListLocalFeedPins() error = %v", err)
	}
	if len(pins) != 1 {
		t.Fatalf("pins = %d, want 1", len(pins))
	}
	if pins[0].DisplayName != "MapNick" {
		t.Fatalf("pins[0].DisplayName = %q, want %q", pins[0].DisplayName, "MapNick")
	}
}
