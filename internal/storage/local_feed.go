package storage

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *Store) CreateLocalFeedPost(ctx context.Context, userID string, text *string, imageURLs []string, radiusM int, expiresAtMs int64, isPinned bool, nowMs int64) (LocalFeedPostRow, []LocalFeedPostImageRow, error) {
	if s == nil || s.db == nil {
		return LocalFeedPostRow{}, nil, fmt.Errorf("db not initialized")
	}
	if userID == "" {
		return LocalFeedPostRow{}, nil, fmt.Errorf("missing userID")
	}
	if radiusM <= 0 {
		return LocalFeedPostRow{}, nil, fmt.Errorf("invalid radiusM")
	}
	if expiresAtMs <= nowMs {
		return LocalFeedPostRow{}, nil, fmt.Errorf("invalid expiresAtMs")
	}

	var normalizedText *string
	if text != nil {
		t := strings.TrimSpace(*text)
		if t != "" {
			normalizedText = &t
		}
	}

	postID := uuid.NewString()
	post := LocalFeedPostRow{
		ID:          postID,
		UserID:      userID,
		Text:        normalizedText,
		RadiusM:     radiusM,
		ExpiresAtMs: expiresAtMs,
		IsPinned:    isPinned,
		CreatedAtMs: nowMs,
		UpdatedAtMs: nowMs,
	}

	txCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	tx, err := s.db.BeginTx(txCtx, nil)
	if err != nil {
		return LocalFeedPostRow{}, nil, err
	}
	defer func() { _ = tx.Rollback() }()

	insertPostQ := `INSERT INTO local_feed_posts (
			id, user_id, text, radius_m, expires_at_ms, is_pinned, created_at_ms, updated_at_ms
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?);`
	pinnedInt := 0
	if isPinned {
		pinnedInt = 1
	}
	if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertPostQ),
		post.ID, post.UserID, post.Text, post.RadiusM, post.ExpiresAtMs, pinnedInt, post.CreatedAtMs, post.UpdatedAtMs,
	); err != nil {
		return LocalFeedPostRow{}, nil, err
	}

	var images []LocalFeedPostImageRow
	for i, url := range imageURLs {
		url = strings.TrimSpace(url)
		if url == "" {
			continue
		}
		img := LocalFeedPostImageRow{
			ID:          uuid.NewString(),
			PostID:      post.ID,
			URL:         url,
			SortOrder:   i,
			CreatedAtMs: nowMs,
		}
		insertImgQ := `INSERT INTO local_feed_post_images (id, post_id, url, sort_order, created_at_ms)
			VALUES (?, ?, ?, ?, ?);`
		if _, err := tx.ExecContext(txCtx, rebindQuery(s.driver, insertImgQ), img.ID, img.PostID, img.URL, img.SortOrder, img.CreatedAtMs); err != nil {
			return LocalFeedPostRow{}, nil, err
		}
		images = append(images, img)
	}

	if err := tx.Commit(); err != nil {
		return LocalFeedPostRow{}, nil, err
	}
	return post, images, nil
}

func (s *Store) DeleteLocalFeedPost(ctx context.Context, userID, postID string) error {
	if s == nil || s.db == nil {
		return fmt.Errorf("db not initialized")
	}
	if userID == "" || postID == "" {
		return fmt.Errorf("missing ids")
	}

	q := `DELETE FROM local_feed_posts WHERE id = ? AND user_id = ?;`
	res, err := s.db.ExecContext(ctx, s.rebind(q), postID, userID)
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("%w: local feed post", ErrNotFound)
	}
	return nil
}

type LocalFeedPostWithImages struct {
	Post   LocalFeedPostRow
	Images []LocalFeedPostImageRow
}

func (s *Store) ListLocalFeedPostsForSource(ctx context.Context, sourceUserID string, atLatE7, atLngE7 *int64, nowMs int64, limit int) ([]LocalFeedPostWithImages, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if sourceUserID == "" {
		return nil, fmt.Errorf("missing sourceUserID")
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	var hb *HomeBaseRow
	if atLatE7 != nil && atLngE7 != nil {
		row, err := s.GetHomeBase(ctx, sourceUserID)
		if err != nil {
			return nil, err
		}
		hb = &row
	}

	q := `SELECT id, user_id, text, radius_m, expires_at_ms, is_pinned, created_at_ms, updated_at_ms
		FROM local_feed_posts
		WHERE user_id = ? AND expires_at_ms > ?
		ORDER BY is_pinned DESC, created_at_ms DESC
		LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q), sourceUserID, nowMs, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var posts []LocalFeedPostRow
	for rows.Next() {
		var (
			p      LocalFeedPostRow
			text   sql.NullString
			pinned int
		)
		if err := rows.Scan(&p.ID, &p.UserID, &text, &p.RadiusM, &p.ExpiresAtMs, &pinned, &p.CreatedAtMs, &p.UpdatedAtMs); err != nil {
			return nil, err
		}
		if text.Valid {
			p.Text = &text.String
		}
		p.IsPinned = pinned != 0

		if hb != nil {
			dist := distanceMetersE7(hb.LatE7, hb.LngE7, *atLatE7, *atLngE7)
			if dist > float64(p.RadiusM) {
				continue
			}
		}

		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(posts) == 0 {
		return nil, nil
	}

	// Load images for posts.
	postIDs := make([]any, 0, len(posts))
	for _, p := range posts {
		postIDs = append(postIDs, p.ID)
	}

	placeholders := strings.TrimRight(strings.Repeat("?,", len(postIDs)), ",")
	imgQ := fmt.Sprintf(`SELECT id, post_id, url, sort_order, created_at_ms
		FROM local_feed_post_images
		WHERE post_id IN (%s)
		ORDER BY post_id ASC, sort_order ASC;`, placeholders)

	imgRows, err := s.db.QueryContext(ctx, s.rebind(imgQ), postIDs...)
	if err != nil {
		return nil, err
	}
	defer imgRows.Close()

	imagesByPost := make(map[string][]LocalFeedPostImageRow, len(posts))
	for imgRows.Next() {
		var img LocalFeedPostImageRow
		if err := imgRows.Scan(&img.ID, &img.PostID, &img.URL, &img.SortOrder, &img.CreatedAtMs); err != nil {
			return nil, err
		}
		imagesByPost[img.PostID] = append(imagesByPost[img.PostID], img)
	}
	if err := imgRows.Err(); err != nil {
		return nil, err
	}

	out := make([]LocalFeedPostWithImages, 0, len(posts))
	for _, p := range posts {
		out = append(out, LocalFeedPostWithImages{
			Post:   p,
			Images: imagesByPost[p.ID],
		})
	}
	return out, nil
}

func (s *Store) ListLocalFeedPins(ctx context.Context, minLatE7, maxLatE7, minLngE7, maxLngE7, centerLatE7, centerLngE7 int64, limit int) ([]LocalFeedPinRow, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("db not initialized")
	}
	if limit <= 0 || limit > 500 {
		limit = 200
	}

	q := `SELECT
			hb.user_id,
			hb.lat_e7,
			hb.lng_e7,
			COALESCE(mp.nickname_override, u.display_name) AS display_name,
			COALESCE(mp.avatar_url_override, u.avatar_url) AS avatar_url,
			hb.updated_at_ms
		FROM home_bases hb
		JOIN users u ON u.id = hb.user_id
		LEFT JOIN user_map_profiles mp ON mp.user_id = hb.user_id
		WHERE hb.lat_e7 >= ? AND hb.lat_e7 <= ? AND hb.lng_e7 >= ? AND hb.lng_e7 <= ?
		ORDER BY ((hb.lat_e7 - ?) * (hb.lat_e7 - ?) + (hb.lng_e7 - ?) * (hb.lng_e7 - ?)) ASC
		LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, s.rebind(q),
		minLatE7, maxLatE7, minLngE7, maxLngE7,
		centerLatE7, centerLatE7, centerLngE7, centerLngE7,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LocalFeedPinRow
	for rows.Next() {
		var (
			p      LocalFeedPinRow
			avatar sql.NullString
		)
		if err := rows.Scan(&p.UserID, &p.LatE7, &p.LngE7, &p.DisplayName, &avatar, &p.UpdatedAtMs); err != nil {
			return nil, err
		}
		if avatar.Valid {
			p.AvatarURL = &avatar.String
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func distanceMetersE7(lat1E7, lng1E7, lat2E7, lng2E7 int64) float64 {
	const earthRadiusMeters = 6371000.0

	lat1 := (float64(lat1E7) / 1e7) * math.Pi / 180.0
	lng1 := (float64(lng1E7) / 1e7) * math.Pi / 180.0
	lat2 := (float64(lat2E7) / 1e7) * math.Pi / 180.0
	lng2 := (float64(lng2E7) / 1e7) * math.Pi / 180.0

	dlat := lat2 - lat1
	dlng := lng2 - lng1

	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dlng/2)*math.Sin(dlng/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}
