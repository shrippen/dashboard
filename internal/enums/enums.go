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
	ServicePGBackWeb     ServiceType = "pgbackweb"
	ServiceMail          ServiceType = "mail"
	ServiceTrueNAS       ServiceType = "truenas"
	ServiceKomodo        ServiceType = "komodo"
	ServicePangolin      ServiceType = "pangolin"
	ServiceAuthentik     ServiceType = "authentik"
	ServicePihole        ServiceType = "pihole"
	ServiceAdGuard       ServiceType = "adguard"
	ServiceNextcloud     ServiceType = "nextcloud"
	ServiceSabnzbd       ServiceType = "sabnzbd"
	ServiceGluetun       ServiceType = "gluetun"
	ServiceDomains       ServiceType = "domains"
	ServiceBlacklist     ServiceType = "blacklist"
	ServiceJSONAPI       ServiceType = "jsonapi"
	ServiceTailscale     ServiceType = "tailscale"
	ServiceGateway       ServiceType = "gateway"
	ServiceMediaServer   ServiceType = "mediaserver"
	ServiceArr           ServiceType = "arr"
	ServiceVaultwarden   ServiceType = "vaultwarden"
	ServiceSpeedtest     ServiceType = "speedtest"
	ServiceGrocy         ServiceType = "grocy"
	ServiceDWD           ServiceType = "dwd"
	ServiceGitHub        ServiceType = "github"
	ServiceTibber        ServiceType = "tibber"
	ServiceCalendar      ServiceType = "calendar"
)

// Services lists every connectable service, in form order.
var Services = []ServiceType{
	ServiceKimai, ServiceInvoiceNinja, ServiceSnipeIT, ServiceDawarich, ServiceGlances,
	ServiceUptimeKuma, ServiceProxmox, ServicePaperless, ServiceCerts,
	ServiceScrutiny, ServiceImmich, ServiceUmami, ServiceFreshRSS, ServiceGitea, ServiceBorgBackup,
	ServiceHomeAssistant, ServiceSure, ServiceLinkwarden, ServicePGBackWeb, ServiceMail,
	ServiceTrueNAS, ServiceKomodo, ServicePangolin, ServiceAuthentik,
	ServicePihole, ServiceAdGuard, ServiceNextcloud, ServiceSabnzbd, ServiceGluetun, ServiceDomains, ServiceBlacklist,
	ServiceTailscale, ServiceGateway, ServiceMediaServer, ServiceArr, ServiceVaultwarden,
	ServiceSpeedtest, ServiceGrocy, ServiceDWD, ServiceGitHub, ServiceTibber, ServiceCalendar,
	ServiceJSONAPI,
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

// HintEvent is one entry of a hint's history.
type HintEvent string

const (
	EventOpened   HintEvent = "opened"   // first finding
	EventResolved HintEvent = "resolved" // rule stopped firing
	EventReopened HintEvent = "reopened" // fired again after resolving
	EventAcked    HintEvent = "acked"
	EventSnoozed  HintEvent = "snoozed"
	EventReset    HintEvent = "reset" // ack or snooze undone
	EventAssigned HintEvent = "assigned"
	EventWork     HintEvent = "work"
	EventNote     HintEvent = "note"
)

// WorkState is how far the assignee got with a hint.
type WorkState string

const (
	WorkOpen     WorkState = "open"
	WorkProgress WorkState = "in_progress"
	WorkDone     WorkState = "done"
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

// MobileMode: how a section shows on narrow screens.
type MobileMode string

const (
	MobileNormal MobileMode = ""
	MobileFirst  MobileMode = "first"
	MobileHide   MobileMode = "hide"
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
