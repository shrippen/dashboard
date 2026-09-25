package web

import (
	"errors"

	"dashboard/internal/services/accounts"
	"dashboard/internal/services/auth"
)

// knownErrors maps service errors without a catalog-key message to their
// catalog key. Errors whose message already is a key pass through.
var knownErrors = []struct {
	err error
	key string
}{
	{accounts.ErrEmailTaken, "account.email_taken"},
	{accounts.ErrPasswordTooShort, "password.too_short"},
	{accounts.ErrWrongPassword, "password.wrong"},
	{auth.ErrThrottled, "login.throttled"},
}

// errKey returns the catalog key for err, for {{t .Error}} in templates.
// e.g. accounts.ErrEmailTaken -> "account.email_taken".
func errKey(err error) string {
	for _, k := range knownErrors {
		if errors.Is(err, k.err) {
			return k.key
		}
	}
	return err.Error()
}
