// Package enums holds domain enums shared across layers.
package enums

// InstanceRole is a user's role at instance level.
type InstanceRole string

const (
	RoleAdmin InstanceRole = "admin"
	RoleUser  InstanceRole = "user"
)

// TeamRole is a user's role inside one team.
type TeamRole string

const (
	TeamOwner  TeamRole = "owner"
	TeamEditor TeamRole = "editor"
	TeamViewer TeamRole = "viewer"
)

// SpaceKind distinguishes the three kinds of spaces.
type SpaceKind string

const (
	SpacePersonal SpaceKind = "personal"
	SpaceTeam     SpaceKind = "team"
	SpaceInstance SpaceKind = "instance"
)

// Right is ordered: a higher right includes all lower ones.
type Right int

const (
	RightNone   Right = 0
	RightView   Right = 10
	RightUse    Right = 20
	RightEdit   Right = 30
	RightManage Right = 40
)

// Key returns the catalog-key suffix for this right ("none", "view", ...),
// used by the shares dialog for both form values and i18n lookups.
func (r Right) Key() string {
	switch r {
	case RightView:
		return "view"
	case RightUse:
		return "use"
	case RightEdit:
		return "edit"
	case RightManage:
		return "manage"
	default:
		return "none"
	}
}

// ResourceKind names what a Share grants a right on.
type ResourceKind string

const (
	ResourceSpace      ResourceKind = "space"
	ResourceBoard      ResourceKind = "board"
	ResourceWidget     ResourceKind = "widget"
	ResourceConnection ResourceKind = "connection"
	ResourceTheme      ResourceKind = "theme"
)

// GranteeKind names who a Share is granted to.
type GranteeKind string

const (
	GranteeUser GranteeKind = "user"
	GranteeTeam GranteeKind = "team"
)

// CredentialMode: one shared secret per connection, or one per user.
type CredentialMode string

const (
	CredentialShared   CredentialMode = "shared"
	CredentialPersonal CredentialMode = "personal"
)

// ServiceType names a supported source service.
type ServiceType string

const (
	ServiceKimai         ServiceType = "kimai"
	ServiceInvoiceNinja  ServiceType = "invoiceninja"
	ServiceSnipeIT       ServiceType = "snipeit"
	ServiceDawarich      ServiceType = "dawarich"
	ServiceGlances       ServiceType = "glances"
	ServiceUptimeKuma    ServiceType = "uptimekuma"
	ServiceProxmox       ServiceType = "proxmox"
	ServicePaperless     ServiceType = "paperless"
	ServiceCerts         ServiceType = "certs"
	ServiceScrutiny      ServiceType = "scrutiny"
	ServiceImmich        ServiceType = "immich"
	ServiceUmami         ServiceType = "umami"
	ServiceFreshRSS      ServiceType = "freshrss"
	ServiceGitea         ServiceType = "gitea"
	ServiceBorgBackup    ServiceType = "borgbackup"
	ServiceHomeAssistant ServiceType = "homeassistant"
	ServiceSure          ServiceType = "sure"
	ServiceLinkwarden    ServiceType = "linkwarden"
)

// Services lists every connectable service, in form order.
var Services = []ServiceType{
	ServiceKimai, ServiceInvoiceNinja, ServiceSnipeIT, ServiceDawarich, ServiceGlances,
	ServiceUptimeKuma, ServiceProxmox, ServicePaperless, ServiceCerts,
	ServiceScrutiny, ServiceImmich, ServiceUmami, ServiceFreshRSS, ServiceGitea, ServiceBorgBackup,
	ServiceHomeAssistant, ServiceSure, ServiceLinkwarden,
}

// Known reports whether s is a connectable service.
func (s ServiceType) Known() bool {
	for _, k := range Services {
		if k == s {
			return true
		}
	}
	return false
}

// Severity of a hint.
type Severity int

const (
	SeverityInfo     Severity = 10
	SeverityWarn     Severity = 20
	SeverityCritical Severity = 30
)

// Key returns the catalog-key suffix ("info", "warn", "critical").
func (s Severity) Key() string {
	switch {
	case s >= SeverityCritical:
		return "critical"
	case s >= SeverityWarn:
		return "warn"
	default:
		return "info"
	}
}

// HintState tracks a hint's acknowledgement.
type HintState string

const (
	HintOpen         HintState = "open"
	HintSnoozed      HintState = "snoozed"
	HintAcknowledged HintState = "acknowledged"
	HintResolved     HintState = "resolved"
)

// HintAckMode: whether acknowledging a team hint applies to the team or one user.
type HintAckMode string

const (
	AckPerUser HintAckMode = "per_user"
	AckTeam    HintAckMode = "team"
)

// AuthMethod names how a session was established.
type AuthMethod string

const (
	AuthPassword AuthMethod = "password"
	AuthOIDC     AuthMethod = "oidc"
	AuthPasskey  AuthMethod = "passkey"
	AuthToken    AuthMethod = "token"
)

// ColorMode is a user's theme preference.
type ColorMode string

const (
	ColorAuto  ColorMode = "auto"
	ColorDark  ColorMode = "dark"
	ColorLight ColorMode = "light"
)

// Locale is a supported UI language.
type Locale string

const (
	LocaleDE Locale = "de"
	LocaleEN Locale = "en"
)

// TokenScope limits what an API/embed token may do.
type TokenScope string

const (
	TokenRead  TokenScope = "read"
	TokenEmbed TokenScope = "embed"
)

// TileSize is a section's widget tile size.
type TileSize string

const (
	TileSmall  TileSize = "small"
	TileMedium TileSize = "medium"
	TileLarge  TileSize = "large"
)

// SortOrder controls how a section orders its widgets.
type SortOrder string

const (
	SortManual       SortOrder = "manual"
	SortAlphabetical SortOrder = "alphabetical"
)

// LinkTarget: where a link widget opens.
type LinkTarget string

const (
	LinkNewTab  LinkTarget = "newtab"
	LinkSameTab LinkTarget = "sametab"
)

// RevisionKind names what a Revision snapshot belongs to.
type RevisionKind string

const (
	RevisionBoard  RevisionKind = "board"
	RevisionWidget RevisionKind = "widget"
	RevisionSpace  RevisionKind = "space"
	RevisionTheme  RevisionKind = "theme"
)
