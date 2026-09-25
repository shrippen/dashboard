package web

import (
	"net/http"

	"dashboard/internal/enums"
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
)

func credShapeOf(service enums.ServiceType) credShape {
	switch service {
	case enums.ServiceFreshRSS, enums.ServiceMail:
		return credUserPass
	case enums.ServiceKomodo:
		return credKeySecret
	default:
		return credSingle
	}
}

// formSecret reads the connection secret from the form, joining a two-part
// credential's fields into the "a:b" shape the drivers expect. Both parts
// empty means "unchanged" (create: no secret), same as the single field.
func formSecret(r *http.Request, service enums.ServiceType) string {
	if credShapeOf(service) == credSingle {
		return r.FormValue("secret")
	}
	a, b := r.FormValue("secret_a"), r.FormValue("secret_b")
	if a == "" && b == "" {
		return ""
	}
	return a + ":" + b
}
