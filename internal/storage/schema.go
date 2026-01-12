package storage

import (
	"context"
	"database/sql"
)

func initSchema(ctx context.Context, db *sql.DB, driver string) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			display_name TEXT NOT NULL,
			avatar_url TEXT,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username ON users(username);`,

		`CREATE TABLE IF NOT EXISTS auth_tokens (
			token TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			device_info TEXT,
			created_at_ms BIGINT NOT NULL,
			expires_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_auth_tokens_user ON auth_tokens(user_id);`,
		`CREATE INDEX IF NOT EXISTS idx_auth_tokens_expires ON auth_tokens(expires_at_ms);`,

		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			participants_hash TEXT NOT NULL UNIQUE,
			user1_id TEXT NOT NULL,
			user2_id TEXT NOT NULL,
			source TEXT NOT NULL DEFAULT 'wechat_code',
			kind TEXT NOT NULL DEFAULT 'direct',
			status TEXT NOT NULL DEFAULT 'active',
			last_message_text TEXT,
			last_message_at_ms BIGINT,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			hidden_by_users TEXT DEFAULT '[]',
			reactivated_at_ms BIGINT,
			FOREIGN KEY(user1_id) REFERENCES users(id),
			FOREIGN KEY(user2_id) REFERENCES users(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user1 ON sessions(user1_id);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user2 ON sessions(user2_id);`,

		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			type TEXT NOT NULL,
			text TEXT,
			meta_json TEXT,
			created_at_ms BIGINT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY(sender_id) REFERENCES users(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_created_at_ms ON messages(session_id, created_at_ms);`,

		`CREATE TABLE IF NOT EXISTS burn_messages (
			message_id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			sender_id TEXT NOT NULL,
			recipient_id TEXT NOT NULL,
			burn_after_ms BIGINT NOT NULL,
			opened_at_ms BIGINT,
			burn_at_ms BIGINT,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(message_id) REFERENCES messages(id) ON DELETE CASCADE,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY(sender_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY(recipient_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_burn_messages_session_created_at_ms ON burn_messages(session_id, created_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_burn_messages_burn_at_ms ON burn_messages(burn_at_ms);`,

		`CREATE TABLE IF NOT EXISTS calls (
			id TEXT PRIMARY KEY,
			group_id TEXT NOT NULL,
			caller_id TEXT NOT NULL,
			callee_id TEXT NOT NULL,
			media_type TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(caller_id) REFERENCES users(id),
			FOREIGN KEY(callee_id) REFERENCES users(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller_id, updated_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee_id, updated_at_ms);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_calls_group_id ON calls(group_id);`,

		`CREATE TABLE IF NOT EXISTS wechat_bindings (
			user_id TEXT PRIMARY KEY,
			openid TEXT NOT NULL UNIQUE,
			session_key TEXT NOT NULL,
			unionid TEXT,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_wechat_bindings_openid ON wechat_bindings(openid);`,

		`CREATE TABLE IF NOT EXISTS session_requests (
			id TEXT PRIMARY KEY,
			requester_id TEXT NOT NULL,
			addressee_id TEXT NOT NULL,
			status TEXT NOT NULL,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(requester_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY(addressee_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_requests_pair ON session_requests(requester_id, addressee_id);`,
		`CREATE INDEX IF NOT EXISTS idx_session_requests_addressee_status ON session_requests(addressee_id, status, updated_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_session_requests_requester_status ON session_requests(requester_id, status, updated_at_ms);`,

		`CREATE TABLE IF NOT EXISTS session_invites (
			code TEXT PRIMARY KEY,
			inviter_id TEXT NOT NULL UNIQUE,
			expires_at_ms BIGINT,
			geo_fence_lat_e7 BIGINT,
			geo_fence_lng_e7 BIGINT,
			geo_fence_radius_m INTEGER,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(inviter_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_invites_inviter ON session_invites(inviter_id);`,

		`CREATE TABLE IF NOT EXISTS home_bases (
			user_id TEXT PRIMARY KEY,
			lat_e7 BIGINT NOT NULL,
			lng_e7 BIGINT NOT NULL,
			last_updated_ymd INTEGER NOT NULL,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_home_bases_lat_lng ON home_bases(lat_e7, lng_e7);`,

		`CREATE TABLE IF NOT EXISTS user_card_profiles (
			user_id TEXT PRIMARY KEY,
			nickname_override TEXT,
			avatar_url_override TEXT,
			profile_json TEXT NOT NULL DEFAULT '{}',
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS user_map_profiles (
			user_id TEXT PRIMARY KEY,
			nickname_override TEXT,
			avatar_url_override TEXT,
			profile_json TEXT NOT NULL DEFAULT '{}',
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,

		`CREATE TABLE IF NOT EXISTS local_feed_posts (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			text TEXT,
			radius_m INTEGER NOT NULL,
			expires_at_ms BIGINT NOT NULL,
			is_pinned INTEGER NOT NULL DEFAULT 0,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_local_feed_posts_user_created_at_ms ON local_feed_posts(user_id, created_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_local_feed_posts_expires_at_ms ON local_feed_posts(expires_at_ms);`,

		`CREATE TABLE IF NOT EXISTS local_feed_post_images (
			id TEXT PRIMARY KEY,
			post_id TEXT NOT NULL,
			url TEXT NOT NULL,
			sort_order INTEGER NOT NULL,
			created_at_ms BIGINT NOT NULL,
			FOREIGN KEY(post_id) REFERENCES local_feed_posts(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_local_feed_post_images_post_sort ON local_feed_post_images(post_id, sort_order);`,

		`CREATE TABLE IF NOT EXISTS relationship_groups (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			name TEXT NOT NULL,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_relationship_groups_user_name ON relationship_groups(user_id, name);`,
		`CREATE INDEX IF NOT EXISTS idx_relationship_groups_user_updated_at_ms ON relationship_groups(user_id, updated_at_ms);`,

		`CREATE TABLE IF NOT EXISTS session_user_meta (
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			note TEXT,
			group_id TEXT,
			tags_json TEXT NOT NULL DEFAULT '[]',
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			PRIMARY KEY(session_id, user_id),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE,
			FOREIGN KEY(group_id) REFERENCES relationship_groups(id) ON DELETE SET NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_session_user_meta_user_updated_at_ms ON session_user_meta(user_id, updated_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_session_user_meta_user_group ON session_user_meta(user_id, group_id);`,

		`CREATE TABLE IF NOT EXISTS session_participants (
			session_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'member',
			status TEXT NOT NULL DEFAULT 'active',
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			PRIMARY KEY(session_id, user_id),
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_session_participants_session_role ON session_participants(session_id, role);`,
		`CREATE INDEX IF NOT EXISTS idx_session_participants_user_status_updated_at_ms ON session_participants(user_id, status, updated_at_ms);`,

		`CREATE TABLE IF NOT EXISTS activities (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL UNIQUE,
			creator_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			start_at_ms BIGINT,
			end_at_ms BIGINT,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE,
			FOREIGN KEY(creator_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_activities_creator_updated_at_ms ON activities(creator_id, updated_at_ms);`,
		`CREATE INDEX IF NOT EXISTS idx_activities_end_at_ms ON activities(end_at_ms);`,

		`CREATE TABLE IF NOT EXISTS activity_invites (
				code TEXT PRIMARY KEY,
				activity_id TEXT NOT NULL UNIQUE,
				expires_at_ms BIGINT,
				geo_fence_lat_e7 BIGINT,
				geo_fence_lng_e7 BIGINT,
				geo_fence_radius_m INTEGER,
				created_at_ms BIGINT NOT NULL,
				updated_at_ms BIGINT NOT NULL,
				FOREIGN KEY(activity_id) REFERENCES activities(id) ON DELETE CASCADE
			);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_activity_invites_activity_id ON activity_invites(activity_id);`,

		`CREATE TABLE IF NOT EXISTS activity_reminders (
				activity_id TEXT NOT NULL,
				user_id TEXT NOT NULL,
				remind_at_ms BIGINT NOT NULL,
				status TEXT NOT NULL DEFAULT 'pending',
				last_error TEXT,
				sent_at_ms BIGINT,
				created_at_ms BIGINT NOT NULL,
				updated_at_ms BIGINT NOT NULL,
				PRIMARY KEY(activity_id, user_id),
				FOREIGN KEY(activity_id) REFERENCES activities(id) ON DELETE CASCADE,
				FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
			);`,
		`CREATE INDEX IF NOT EXISTS idx_activity_reminders_status_remind_at_ms ON activity_reminders(status, remind_at_ms);`,
	}

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	if err := applyMigrations(ctx, db, driver); err != nil {
		return err
	}
	return nil
}
