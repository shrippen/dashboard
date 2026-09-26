package widgets

// Topic groups widget types by subject in the gallery ("Kachel hinzufügen").
// Unlike Category, which says how a type gets its data, a topic says what
// it is about.
type Topic string

const (
	TopicOverview Topic = "overview"
	TopicWork     Topic = "work"
	TopicAnalysis Topic = "analysis"
	TopicHomelab  Topic = "homelab"
	TopicNetwork  Topic = "network"
	TopicSecurity Topic = "security"
	TopicMedia    Topic = "media"
	TopicHome     Topic = "home"
	TopicWorld    Topic = "world"
	TopicDev      Topic = "dev"
)

// Topics lists the topics in gallery order.
var Topics = []Topic{TopicOverview, TopicWork, TopicAnalysis, TopicHomelab, TopicNetwork,
	TopicSecurity, TopicMedia, TopicHome, TopicWorld, TopicDev}

// topicOf maps each type key to its topic; unlisted types land in overview.
var topicOf = map[string]Topic{
	"greeting": TopicOverview, "hints": TopicOverview, "week_story": TopicOverview, "conn_health": TopicOverview,
	"clock": TopicOverview, "calendar": TopicOverview, "note": TopicOverview, "list": TopicOverview,
	"link": TopicOverview, "deadlines": TopicOverview,

	"kimai_timer": TopicWork, "kimai_week": TopicWork, "kimai_split": TopicWork, "heatmap": TopicWork,
	"unbilled_age": TopicWork, "cashflow": TopicWork, "invoice_aging": TopicWork, "mail_invoices": TopicWork,
	"dawarich_day": TopicWork, "paperless_inbox": TopicWork,

	"kpi": TopicAnalysis, "chart": TopicAnalysis, "table": TopicAnalysis, "trend": TopicAnalysis,
	"progress": TopicAnalysis, "jsonapi": TopicAnalysis, "custom_api": TopicAnalysis,

	"sysinfo": TopicHomelab, "glances_chart": TopicHomelab, "monitors": TopicHomelab, "disks": TopicHomelab,
	"truenas_pools": TopicHomelab, "komodo_stacks": TopicHomelab, "backups": TopicHomelab,
	"storage_forecast": TopicHomelab, "updates": TopicHomelab, "update_window": TopicHomelab,
	"homelab_cost": TopicHomelab,

	"adguard": TopicNetwork, "pihole": TopicNetwork, "gateway": TopicNetwork, "vpn": TopicNetwork,
	"tailscale": TopicNetwork, "speedtest": TopicNetwork, "speed_history": TopicNetwork, "public_ip": TopicNetwork,

	"authentik_logins": TopicSecurity, "vaultwarden_2fa": TopicSecurity, "expiry": TopicSecurity,

	"mediaserver": TopicMedia, "arr_upcoming": TopicMedia, "sabnzbd": TopicMedia, "freshrss_feeds": TopicMedia,
	"rss": TopicMedia, "linkwarden": TopicMedia, "apod": TopicMedia, "xkcd": TopicMedia, "joke": TopicMedia,
	"image": TopicMedia,

	"hass": TopicHome, "grocy": TopicHome, "energy": TopicHome, "weather": TopicHome, "dwd": TopicHome,

	"transit": TopicWorld, "flights": TopicWorld, "holidays": TopicWorld, "rates": TopicWorld,
	"stocks": TopicWorld, "crypto": TopicWorld,

	"gitea_reviews": TopicDev, "github": TopicDev, "iframe": TopicDev,
}

// TopicOf returns the gallery topic of a type key.
func TopicOf(key string) Topic {
	if topic, ok := topicOf[key]; ok {
		return topic
	}
	return TopicOverview
}
