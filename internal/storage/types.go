package storage

import "errors"

const (
	SessionStatusActive   = "active"
	SessionStatusArchived = "archived"
)

const (
	MessageTypeText   = "text"
	MessageTypeImage  = "image"
	MessageTypeFile   = "file"
	MessageTypeSystem = "system"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrUsernameExists  = errors.New("username exists")
	ErrCannotChatSelf  = errors.New("cannot chat self")
	ErrSessionExists   = errors.New("session exists")
	ErrAccessDenied    = errors.New("access denied")
	ErrTokenInvalid    = errors.New("token invalid")
	ErrTokenExpired    = errors.New("token expired")
)

type UserRow struct {
	ID           string
	Username     string
	PasswordHash string
	DisplayName  string
	AvatarURL    *string
	CreatedAtMs  int64
	UpdatedAtMs  int64
}

type AuthTokenRow struct {
	Token       string
	UserID      string
	DeviceInfo  *string
	CreatedAtMs int64
	ExpiresAtMs int64
}

type SessionRow struct {
	ID               string
	ParticipantsHash string
	User1ID          string
	User2ID          string
	Status           string
	LastMessageText  *string
	LastMessageAtMs  *int64
	CreatedAtMs      int64
	UpdatedAtMs      int64
}

type MessageRow struct {
	ID          string
	SessionID   string
	SenderID    string
	Type        string
	Text        *string
	MetaJSON    []byte
	CreatedAtMs int64
}
