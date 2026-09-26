package web

import (
	"net/http"

	"andon/internal/enums"
)

// credShape says whether a service's credential is one token or two named
// parts. The drivers still take a single "a:b" secret (see
// internal/sources/mail.go, internal/drivers/services/{homelab,infra}.go);
// this only changes how the form collects it, so users don't have to build
// that string by hand.
type credShape string

const (
	credSingle    credShape = "single"
	credUserPass  credShape = "userpass"
	credKeySecret credShape = "keysecret"
	credNone      credShape = "none"    // no login at all: no token field, no mode
	credTokenID   credShape = "tokenid" // token ID and secret, joined "id=secret" (Proxmox)
	credICal      credShape = "ical"    // one field, but it holds a private iCal address
	credPassword  credShape = "password" // one field, but a password, not a token (Pi-hole)
)

// defaultURLs are the fixed API addresses of hosted services, filled in
// when such a connection is set up.
var defaultURLs = map[enums.ServiceType]string{
	enums.ServiceGitHub:    "https://api.github.com",
	enums.ServiceTibber:    "https://api.tibber.com/v1-beta/gql",
	enums.ServiceDWD:       "https://api.brightsky.dev",
	enums.ServiceTailscale: "https://api.tailscale.com",
}

func defaultURL(service enums.ServiceType) string {
	return defaultURLs[service]
}

func credShapeOf(service enums.ServiceType) credShape {
	switch service {
	case enums.ServiceFreshRSS, enums.ServiceMail, enums.ServiceAdGuard, enums.ServiceUmami, enums.ServiceNextcloud:
		return credUserPass
	case enums.ServiceKomodo, enums.ServiceGateway:
		return credKeySecret
	case enums.ServiceProxmox:
		return credTokenID
	case enums.ServiceCalendar:
		return credICal
	case enums.ServicePihole:
		return credPassword
	case enums.ServiceScrutiny, enums.ServiceCerts, enums.ServiceDomains, enums.ServiceBlacklist,
		enums.ServiceDWD, enums.ServicePGBackWeb:
		return credNone
	default:
		return credSingle
	}
}

// formSecret reads the connection secret from the form, joining a two-part
// credential's fields into the "a:b" shape the drivers expect. Both parts
// empty means "unchanged" (create: no secret), same as the single field.
func formSecret(r *http.Request, service enums.ServiceType) string {
	shape := credShapeOf(service)
	switch shape {
	case credNone:
		return ""
	case credSingle, credICal, credPassword:
		return r.FormValue("secret")
	}
	a, b := r.FormValue("secret_a"), r.FormValue("secret_b")
	// First field empty: the service takes a single key instead, e.g.
	// Umami Cloud's API key or a Nextcloud serverinfo token.
	if a == "" {
		return b
	}
	if shape == credTokenID {
		return a + "=" + b
	}
	return a + ":" + b
}
