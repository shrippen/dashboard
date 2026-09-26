package web

import "andon/internal/enums"

// projectURLs link each service's setup screen to the project's website.
// Built-in checks (certificates, domains, blocklists, JSON API, calendar,
// mail) and the multi-product gateway have none.
var projectURLs = map[enums.ServiceType]string{
	enums.ServiceKimai:         "https://www.kimai.org",
	enums.ServiceInvoiceNinja:  "https://invoiceninja.com",
	enums.ServiceSnipeIT:       "https://snipeitapp.com",
	enums.ServiceDawarich:      "https://dawarich.app",
	enums.ServiceGlances:       "https://github.com/nicolargo/glances",
	enums.ServiceUptimeKuma:    "https://uptime.kuma.pet",
	enums.ServiceProxmox:       "https://www.proxmox.com/en/proxmox-virtual-environment",
	enums.ServicePaperless:     "https://docs.paperless-ngx.com",
	enums.ServiceScrutiny:      "https://github.com/AnalogJ/scrutiny",
	enums.ServiceImmich:        "https://immich.app",
	enums.ServiceUmami:         "https://umami.is",
	enums.ServiceFreshRSS:      "https://freshrss.org",
	enums.ServiceGitea:         "https://about.gitea.com",
	enums.ServiceBorgBackup:    "https://www.borgbackup.org",
	enums.ServiceHomeAssistant: "https://www.home-assistant.io",
	enums.ServiceSure:          "https://github.com/we-promise/sure",
	enums.ServiceLinkwarden:    "https://linkwarden.app",
	enums.ServicePGBackWeb:     "https://github.com/eduardolat/pgbackweb",
	enums.ServiceTrueNAS:       "https://www.truenas.com",
	enums.ServiceKomodo:        "https://komo.do",
	enums.ServicePangolin:      "https://github.com/fosrl/pangolin",
	enums.ServiceAuthentik:     "https://goauthentik.io",
	enums.ServicePihole:        "https://pi-hole.net",
	enums.ServiceAdGuard:       "https://github.com/AdguardTeam/AdGuardHome",
	enums.ServiceNextcloud:     "https://nextcloud.com",
	enums.ServiceSabnzbd:       "https://sabnzbd.org",
	enums.ServiceGluetun:       "https://github.com/qdm12/gluetun",
	enums.ServiceTailscale:     "https://tailscale.com",
	enums.ServiceMediaServer:   "https://jellyfin.org",
	enums.ServiceArr:           "https://wiki.servarr.com",
	enums.ServiceVaultwarden:   "https://github.com/dani-garcia/vaultwarden",
	enums.ServiceSpeedtest:     "https://github.com/alexjustesen/speedtest-tracker",
	enums.ServiceGrocy:         "https://grocy.info",
	enums.ServiceDWD:           "https://brightsky.dev",
	enums.ServiceGitHub:        "https://github.com",
	enums.ServiceTibber:        "https://tibber.com",
}

func projectURL(service enums.ServiceType) string {
	return projectURLs[service]
}

// namedLink is a further project a service entry covers.
type namedLink struct {
	Name, URL string
}

// alsoLinks are the second tool behind one service, e.g. MySpeed next to
// Speedtest Tracker.
var alsoLinks = map[enums.ServiceType][]namedLink{
	enums.ServiceSpeedtest:   {{"MySpeed", "https://github.com/gnmyt/MySpeed"}},
	enums.ServiceMediaServer: {{"Plex", "https://www.plex.tv"}},
}

func alsoLinksOf(service enums.ServiceType) []namedLink {
	return alsoLinks[service]
}
