package storage

import (
	"context"
	"database/sql"
)

func initSchema(ctx context.Context, db *sql.DB) error {
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
			status TEXT NOT NULL DEFAULT 'active',
			last_message_text TEXT,
			last_message_at_ms BIGINT,
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
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
			created_at_ms BIGINT NOT NULL,
			updated_at_ms BIGINT NOT NULL,
			FOREIGN KEY(inviter_id) REFERENCES users(id) ON DELETE CASCADE
		);`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_session_invites_inviter ON session_invites(inviter_id);`,
	}

	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}
