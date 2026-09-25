// Package model holds plain domain structs mirroring the database schema.
//
//	User ──Membership(role)──► Team
//	  │                          │
//	  └── Space(personal)        └── Space(team)          Space(instance)
//	         │
//	         ├── Connection ── UserCredential (personal tokens)
//	         ├── Widget ◄── Placement ── Section ── Board ◄── Overlay (per user)
//	         ├── Theme
//	         └── Hint ── HintMark (per user or team-wide)
//
//	Share: resource → user|team with a Right.
package model

import (
	"time"

	"dashboard/internal/enums"
)

type User struct {
	ID            int64
	Email         string
	Name          string
	PasswordHash  string // "" = no password (OIDC-only account)
	Role          enums.InstanceRole
	IsActive      bool
	IsBreakglass  bool
	Locale        enums.Locale
	ColorMode     enums.ColorMode
	ThemeID       *int64
	StartBoardID  *int64
	SearchEngine  string
	Prefs         map[string]any
	TOTPSecretEnc []byte
	TOTPEnabled   bool
	RecoveryCodes []string
	OIDCSub       string
	CreatedAt     time.Time
	LastLoginAt   *time.Time
}

type Team struct {
	ID        int64
	Name      string
	CreatedAt time.Time
}

type Membership struct {
	ID     int64
	UserID int64
	TeamID int64
	Role   enums.TeamRole
}

type Space struct {
	ID          int64
	Kind        enums.SpaceKind
	Name        string
	OwnerUserID *int64
	TeamID      *int64
	Settings    map[string]any
	Version     int
}

type Connection struct {
	ID             int64
	SpaceID        int64
	Key            string
	Name           string
	Service        string
	URL            string
	CredentialMode enums.CredentialMode
	SecretEnc      []byte
	Options        map[string]any
	VerifyTLS      bool
	CreatedAt      time.Time
	SecretAt       time.Time // zero = unknown
	SecretExpires  string    // "2026-12-31", "" = unknown
	DailyBudget    int       // fetches per day, 0 = unlimited
}

type UserCredential struct {
	ID           int64
	ConnectionID int64
	UserID       int64
	SecretEnc    []byte
	SecretAt     time.Time // zero = unknown
}

type Widget struct {
	ID           int64
	SpaceID      int64
	Key          string
	Type         string
	Title        string
	Config       map[string]any
	ConnectionID *int64
	MinTeamRole  *enums.TeamRole
	Version      int
	UpdatedAt    time.Time
}

type Board struct {
	ID          int64
	SpaceID     int64
	Slug        string
	Name        string
	Position    int
	ThemeID     *int64
	IsTemplate  bool
	MinTeamRole *enums.TeamRole
	Version     int
	UpdatedAt   time.Time

	Sections []Section // populated by repos.Board
}

type Section struct {
	ID        int64
	BoardID   int64
	Title     string
	Position  int
	Cols      *int
	Size      enums.TileSize
	Sort      enums.SortOrder
	Collapsed bool
	Area      string
	Span      int    // quarters of the main column, 0 = full width
	Rows      int    // grid rows, 0 = one
	Color     string // theme color token, "" = none
	Mobile    enums.MobileMode

	Placements []Placement // populated by repos.Board
}

type Placement struct {
	ID        int64
	SectionID int64
	WidgetID  int64
	Position  int

	Widget *Widget // populated by repos.Board / repos.Placement
}

type Overlay struct {
	ID      int64
	UserID  int64
	BoardID int64
	Data    map[string]any
}

type Share struct {
	ID           int64
	ResourceKind enums.ResourceKind
	ResourceID   int64
	GranteeKind  enums.GranteeKind
	GranteeID    int64
	Right        enums.Right
	CreatedBy    *int64
}

type Revision struct {
	ID        int64
	Kind      enums.RevisionKind
	EntityID  int64
	SpaceID   int64
	UserID    *int64
	Version   int
	Data      map[string]any
	CreatedAt time.Time
}

type Theme struct {
	ID        int64
	SpaceID   *int64
	Slug      string
	Name      string
	Builtin   bool
	Contract  int
	Dark      map[string]any
	Light     map[string]any
	CustomCSS string
	Fonts     []string
	Digest    string
	Version   int
	UpdatedAt time.Time
}

type LoginSession struct {
	ID         int64
	TokenHash  string
	UserID     int64
	Method     enums.AuthMethod
	CSRF       string
	CreatedAt  time.Time
	LastSeen   time.Time
	ExpiresAt  time.Time
	IP         string
	UserAgent  string
	Pending2FA bool
	IDToken    string
}

type ApiToken struct {
	ID         int64
	UserID     int64
	Name       string
	TokenHash  string
	Prefix     string
	Scope      enums.TokenScope
	BoardIDs   []int64
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

type Invite struct {
	ID        int64
	Email     string
	TokenHash string
	Role      enums.InstanceRole
	Teams     []InviteTeam
	CreatedBy *int64
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// InviteTeam is one team the invitee joins on acceptance (JSON shape kept
// from the Python version: {"team": name, "role": role}).
type InviteTeam struct {
	Team string         `json:"team"`
	Role enums.TeamRole `json:"role"`
}

type ResetToken struct {
	ID        int64
	UserID    int64
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
}

// HookEvent is one event a service pushed to the dashboard.
type HookEvent struct {
	ID           int64
	ConnectionID int64
	Event        string // e.g. "execution_failed"
	Subject      string // e.g. the backup's name
	At           time.Time
}

// Passkey is a WebAuthn credential; Data is the library's credential JSON.
type Passkey struct {
	ID         int64
	UserID     int64
	CredID     string // base64url
	Name       string
	Data       string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type AuditEntry struct {
	ID     int64
	At     time.Time
	UserID *int64
	Action string
	Target string
	Detail map[string]any
	IP     string
}

type CacheEntry struct {
	Key       string
	Source    string
	FetchedAt time.Time
	OkAt      *time.Time
	Data      map[string]any
	Error     string
}

type MetricPoint struct {
	ID     int64
	Scope  string
	Metric string
	Day    string
	Value  float64
}

type Hint struct {
	ID           int64
	SpaceID      int64
	UserID       *int64
	Fingerprint  string
	Rule         string
	Severity     enums.Severity
	Message      string
	Params       map[string]any
	ActionURL    string
	ActionLabel  string
	Due          string
	Sources      []string
	ConnectionID *int64
	FirstSeen    time.Time
	LastSeen     time.Time
	ResolvedAt   *time.Time
}

type HintMark struct {
	ID     int64
	HintID int64
	UserID *int64
	State  enums.HintState
	Until  *time.Time
	At     time.Time
}

type NotifyChannel struct {
	ID          int64
	UserID      int64
	Name        string
	URLEnc      []byte
	MinSeverity enums.Severity
	Enabled     bool
	Sources     []string // subscribed sources, empty = all
}

// HintEvent is one entry of a hint's history.
type HintEvent struct {
	ID     int64
	HintID int64
	Kind   enums.HintEvent
	UserID *int64
	Note   string
	At     time.Time
}

// HintWork is a hint's assignment.
type HintWork struct {
	HintID     int64
	AssigneeID *int64
	State      enums.WorkState
	At         time.Time
}

type NotifyLog struct {
	ID     int64
	UserID int64
	HintID int64
	SentAt time.Time
}
