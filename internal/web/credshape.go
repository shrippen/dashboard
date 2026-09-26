package web

import (
	"errors"
	"net/http"
	"strings"

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
	credNone      credShape = "none"     // no login at all: no token field, no mode
	credTokenID   credShape = "tokenid"  // token ID and secret, joined "id=secret" (Proxmox)
	credICal      credShape = "ical"     // one field, but it holds a private iCal address
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

// errCredIncomplete: only one of two required parts was filled in.
var errCredIncomplete = errors.New("conn.cred_incomplete")

// singleKeyToo lists two-part services that also take one key alone,
// entered with the first field empty (Umami Cloud API key, Nextcloud
// serverinfo token, pfSense and UniFi keys).
var singleKeyToo = map[enums.ServiceType]bool{
	enums.ServiceUmami: true, enums.ServiceNextcloud: true, enums.ServiceGateway: true,
}

// formSecret reads the connection secret from the form, joining a two-part
// credential's fields into the "a:b" shape the drivers expect. Both parts
// empty means "unchanged" (create: no secret), same as the single field.
func formSecret(r *http.Request, service enums.ServiceType) (string, error) {
	shape := credShapeOf(service)
	switch shape {
	case credNone:
		return "", nil
	case credSingle, credICal, credPassword:
		return r.FormValue("secret"), nil
	}

	a, b := r.FormValue("secret_a"), r.FormValue("secret_b")
	switch {
	case a == "" && b == "":
		return "", nil
	case a == "" && singleKeyToo[service]:
		return b, nil
	case a == "" || b == "":
		return "", errCredIncomplete
	case shape == credTokenID:
		return a + "=" + b, nil
	}
	return a + ":" + b, nil
}

// setupField is a connection option the setup form asks for directly,
// because the service does not work without it.
type setupField struct {
	Key   string // option key, form field "opt_<key>"
	Label string // catalog key
}

// setupFields are those options per service, e.g. Pangolin's organisation.
var setupFields = map[enums.ServiceType][]setupField{
	enums.ServicePangolin: {{Key: "org", Label: "conn.pangolin_org"}},
}

func setupFieldsOf(service enums.ServiceType) []setupField {
	return setupFields[service]
}

// formOptions merges the setup fields sent with the form into options; nil
// means "no change" (the service has none, or none were sent).
func formOptions(r *http.Request, service enums.ServiceType, options map[string]any) map[string]any {
	fields := setupFields[service]
	if len(fields) == 0 {
		return nil
	}
	out := map[string]any{}
	for k, v := range options {
		out[k] = v
	}
	changed := false
	for _, f := range fields {
		if _, sent := r.Form["opt_"+f.Key]; !sent {
			continue
		}
		changed = true
		if v := strings.TrimSpace(r.FormValue("opt_" + f.Key)); v != "" {
			out[f.Key] = v
		} else {
			delete(out, f.Key)
		}
	}
	if !changed {
		return nil
	}
	return out
}
