package web

import (
	"errors"

	"andon/internal/services/accounts"
	"andon/internal/services/auth"
	"andon/internal/services/shares"
	"andon/internal/services/teams"
	"andon/internal/services/widgetlib"
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
	{auth.ErrOIDCOnly, "login.oidc_only"},
	{auth.ErrTOTPInvalid, "totp.invalid"},
	{teams.ErrNameMissing, "team.name_missing"},
	{teams.ErrNameTaken, "team.name_taken"},
	{teams.ErrNotFound, "team.not_found"},
	{teams.ErrDenied, "error.denied"},
	{shares.ErrRight, "share.right_invalid"},
	{widgetlib.ErrUnknownType, "widget.unknown_type"},
	{widgetlib.ErrConnRequired, "widget.connection_required"},
	{widgetlib.ErrConnMissing, "widget.connection_missing"},
	{widgetlib.ErrConnWrongService, "widget.connection_type"},
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
