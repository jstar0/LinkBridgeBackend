package storage

import "errors"

const (
	SessionStatusActive   = "active"
	SessionStatusArchived = "archived"
)

const (
	SessionSourceWeChatCode = "wechat_code"
	SessionSourceMap        = "map"
	SessionSourceActivity   = "activity"
	SessionSourceManual     = "manual"
)

const (
	SessionKindDirect = "direct"
	SessionKindGroup  = "group"
)

const (
	MessageTypeText   = "text"
	MessageTypeImage  = "image"
	MessageTypeFile   = "file"
	MessageTypeSystem = "system"
	MessageTypeBurn   = "burn"
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
	SessionRequestStatusPending  = "pending"
	SessionRequestStatusAccepted = "accepted"
	SessionRequestStatusRejected = "rejected"
	SessionRequestStatusCanceled = "canceled"
)

const (
	SessionRequestSourceWeChatCode = "wechat_code"
	SessionRequestSourceMap        = "map"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrUsernameExists    = errors.New("username exists")
	ErrCannotChatSelf    = errors.New("cannot chat self")
	ErrSessionExists     = errors.New("session exists")
	ErrSessionNotFound   = errors.New("session not found")
	ErrAccessDenied      = errors.New("access denied")
	ErrTokenInvalid      = errors.New("token invalid")
	ErrTokenExpired      = errors.New("token expired")
	ErrInvalidState      = errors.New("invalid state")
	ErrWeChatNotBound    = errors.New("wechat not bound")
	ErrRequestExists     = errors.New("session request exists")
	ErrInviteInvalid     = errors.New("session invite invalid")
	ErrInviteExpired     = errors.New("invite expired")
	ErrGeoFenceRequired  = errors.New("geo-fence location required")
	ErrGeoFenceForbidden = errors.New("geo-fence forbidden")
	ErrSessionArchived   = errors.New("session archived")
	ErrRateLimited       = errors.New("rate limited")
	ErrCooldownActive    = errors.New("cooldown active")
	ErrHomeBaseLimited   = errors.New("home base update limited")
	ErrGroupExists       = errors.New("relationship group exists")
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
	Source           string
	Kind             string
	Status           string
	LastMessageText  *string
	LastMessageAtMs  *int64
	CreatedAtMs      int64
	UpdatedAtMs      int64
	HiddenByUsers    *string
	ReactivatedAtMs  *int64
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

type BurnMessageRow struct {
	MessageID   string
	SessionID   string
	SenderID    string
	RecipientID string
	BurnAfterMs int64
	OpenedAtMs  *int64
	BurnAtMs    *int64
	CreatedAtMs int64
	UpdatedAtMs int64
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

type SessionRequestRow struct {
	ID                  string
	RequesterID         string
	AddresseeID         string
	Status              string
	Source              string
	VerificationMessage *string
	CreatedAtMs         int64
	UpdatedAtMs         int64
	LastOpenedAtMs      int64
}

type GeoFence struct {
	LatE7   int64
	LngE7   int64
	RadiusM int
}

type SessionInviteRow struct {
	Code        string
	InviterID   string
	ExpiresAtMs *int64
	GeoFence    *GeoFence
	CreatedAtMs int64
	UpdatedAtMs int64
}

type HomeBaseRow struct {
	UserID           string
	LatE7            int64
	LngE7            int64
	LastUpdatedYMD   int
	DailyUpdateCount int
	CreatedAtMs      int64
	UpdatedAtMs      int64
}

type UserProfileRow struct {
	UserID            string
	NicknameOverride  *string
	AvatarURLOverride *string
	ProfileJSON       string
	CreatedAtMs       int64
	UpdatedAtMs       int64
}

type RelationshipGroupRow struct {
	ID          string
	UserID      string
	Name        string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type SessionUserMetaRow struct {
	SessionID   string
	UserID      string
	Note        *string
	GroupID     *string
	GroupName   *string
	TagsJSON    string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type SessionParticipantRow struct {
	SessionID   string
	UserID      string
	Role        string
	Status      string
	CreatedAtMs int64
	UpdatedAtMs int64
}

type ActivityRow struct {
	ID          string
	SessionID   string
	CreatorID   string
	Title       string
	Description *string
	StartAtMs   *int64
	EndAtMs     *int64
	CreatedAtMs int64
	UpdatedAtMs int64
}

type ActivityInviteRow struct {
	Code        string
	ActivityID  string
	ExpiresAtMs *int64
	GeoFence    *GeoFence
	CreatedAtMs int64
	UpdatedAtMs int64
}

const (
	ActivityReminderStatusPending  = "pending"
	ActivityReminderStatusSent     = "sent"
	ActivityReminderStatusFailed   = "failed"
	ActivityReminderStatusCanceled = "canceled"
)

type ActivityReminderRow struct {
	ActivityID  string
	UserID      string
	RemindAtMs  int64
	Status      string
	LastError   *string
	SentAtMs    *int64
	CreatedAtMs int64
	UpdatedAtMs int64
}

type LocalFeedPostRow struct {
	ID          string
	UserID      string
	Text        *string
	RadiusM     int
	ExpiresAtMs int64
	IsPinned    bool
	CreatedAtMs int64
	UpdatedAtMs int64
}

type LocalFeedPostImageRow struct {
	ID          string
	PostID      string
	URL         string
	SortOrder   int
	CreatedAtMs int64
}

type LocalFeedPinRow struct {
	UserID      string
	LatE7       int64
	LngE7       int64
	DisplayName string
	AvatarURL   *string
	UpdatedAtMs int64
}
