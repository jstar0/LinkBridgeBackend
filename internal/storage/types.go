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

const (
	CallMediaTypeVoice = "voice"
	CallMediaTypeVideo = "video"
)

const (
	CallStatusInviting = "inviting"
	CallStatusAccepted = "accepted"
	CallStatusRejected = "rejected"
	CallStatusCanceled = "canceled"
	CallStatusEnded    = "ended"
	CallStatusMissed   = "missed"
)

const (
	FriendRequestStatusPending  = "pending"
	FriendRequestStatusAccepted = "accepted"
	FriendRequestStatusRejected = "rejected"
	FriendRequestStatusCanceled = "canceled"
)

var (
	ErrNotFound       = errors.New("not found")
	ErrUsernameExists = errors.New("username exists")
	ErrCannotChatSelf = errors.New("cannot chat self")
	ErrSessionExists  = errors.New("session exists")
	ErrAccessDenied   = errors.New("access denied")
	ErrTokenInvalid   = errors.New("token invalid")
	ErrTokenExpired   = errors.New("token expired")
	ErrInvalidState   = errors.New("invalid state")
	ErrWeChatNotBound = errors.New("wechat not bound")
	ErrAlreadyFriends = errors.New("already friends")
	ErrRequestExists  = errors.New("friend request exists")
	ErrInviteInvalid  = errors.New("friend invite invalid")
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

type CallRow struct {
	ID          string
	GroupID     string
	CallerID    string
	CalleeID    string
	MediaType   string
	Status      string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type WeChatBindingRow struct {
	UserID      string
	OpenID      string
	SessionKey  string
	UnionID     *string
	UpdatedAtMs int64
}

type FriendRequestRow struct {
	ID          string
	RequesterID string
	AddresseeID string
	Status      string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type FriendInviteRow struct {
	Code        string
	InviterID   string
	CreatedAtMs int64
	UpdatedAtMs int64
}
