package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"dashboard/internal/enums"
)

func TestCredShapeOf(t *testing.T) {
	cases := []struct {
		service enums.ServiceType
		want    credShape
	}{
		{enums.ServiceFreshRSS, credUserPass},
		{enums.ServiceMail, credUserPass},
		{enums.ServiceKomodo, credKeySecret},
		{enums.ServiceKimai, credSingle},
	}
	for _, c := range cases {
		if got := credShapeOf(c.service); got != c.want {
			t.Errorf("credShapeOf(%s) = %s, want %s", c.service, got, c.want)
		}
	}
}

func formRequest(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(values.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

func TestFormSecretJoinsTwoPartCredential(t *testing.T) {
	r := formRequest(t, url.Values{"secret_a": {"bob"}, "secret_b": {"pw123"}})
	if got := formSecret(r, enums.ServiceFreshRSS); got != "bob:pw123" {
		t.Fatalf("formSecret: %q", got)
	}
}

func TestFormSecretSingleField(t *testing.T) {
	r := formRequest(t, url.Values{"secret": {"tok"}})
	if got := formSecret(r, enums.ServiceKimai); got != "tok" {
		t.Fatalf("formSecret: %q", got)
	}
}

func TestFormSecretBothPartsEmptyMeansUnchanged(t *testing.T) {
	r := formRequest(t, url.Values{})
	if got := formSecret(r, enums.ServiceFreshRSS); got != "" {
		t.Fatalf("formSecret: %q, want empty", got)
	}
}
