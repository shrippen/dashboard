// Package invites handles invitations and password resets: one-time links
// delivered by mail (or shown to the admin when SMTP is not set up).
package invites

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	authrepo "andon/internal/repos/auth"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/audit"
	"andon/internal/services/mail"
)

const (
	inviteValidity     = 7 * 24 * time.Hour
	resetValidity      = 2 * time.Hour
	adminResetValidity = 48 * time.Hour
)

// Errors carry catalog keys so the web layer can show them translated.
var (
	ErrDenied        = errors.New("error.denied")
	ErrEmailTaken    = errors.New("account.email_taken")
	ErrInviteInvalid = errors.New("invite.invalid")
	ErrResetInvalid  = errors.New("reset.invalid")
)

// View is an open invite for the admin list and the accept form.
type View struct {
	ID        int64
	Email     string
	Role      enums.InstanceRole
	Teams     []model.InviteTeam
	ExpiresAt time.Time
}

func now() time.Time { return time.Now().UTC() }

func toView(i *model.Invite) View {
	return View{ID: i.ID, Email: i.Email, Role: i.Role, Teams: i.Teams, ExpiresAt: i.ExpiresAt}
}

func link(kind, token string) string {
	return strings.TrimRight(mail.BaseURL(), "/") + "/" + kind + "/" + token
}

// Create lets an admin invite a person. Returns the link (also mailed if
// SMTP is set up).
func Create(d *sql.DB, who *access.Principal, email string, role enums.InstanceRole, teams []model.InviteTeam, locale enums.Locale) (string, error) {
	if !who.IsAdmin() {
		return "", ErrDenied
	}
	email = strings.TrimSpace(email)
	token := crypto.NewToken()

	err := db.WithTx(d, func(tx *sql.Tx) error {
		existing, err := users.ByEmail(tx, email)
		if err != nil {
			return err
		}
		if existing != nil {
			return ErrEmailTaken
		}
		inv := &model.Invite{
			Email: email, TokenHash: crypto.TokenHash(token), Role: role, Teams: teams,
			CreatedBy: &who.UserID, ExpiresAt: now().Add(inviteValidity),
		}
		if err := authrepo.AddInvite(tx, inv); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "invite.created", email, "", nil)
	})
	if err != nil {
		return "", err
	}

	url := link("invite", token)
	return url, mail.Invite(email, url, who.Name, locale)
}

// Pending lists open invites. Admin only.
func Pending(d *sql.DB, who *access.Principal) ([]View, error) {
	if !who.IsAdmin() {
		return nil, ErrDenied
	}
	open, err := authrepo.OpenInvites(d)
	if err != nil {
		return nil, err
	}
	out := make([]View, 0, len(open))
	for _, i := range open {
		out = append(out, toView(i))
	}
	return out, nil
}

// Revoke deletes an open invite. Admin only.
func Revoke(d *sql.DB, who *access.Principal, inviteID int64) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		if err := authrepo.RemoveInvite(tx, inviteID); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "invite.revoked", strconv.FormatInt(inviteID, 10), "", nil)
	})
}

func usableInvite(q db.Queryer, token string) (*model.Invite, error) {
	inv, err := authrepo.InviteByHash(q, crypto.TokenHash(token))
	if err != nil {
		return nil, err
	}
	if inv == nil || inv.UsedAt != nil || inv.ExpiresAt.Before(now()) {
		return nil, nil
	}
	return inv, nil
}

// Peek returns the invite behind token, or nil if unusable.
func Peek(d *sql.DB, token string) (*View, error) {
	inv, err := usableInvite(d, token)
	if err != nil || inv == nil {
		return nil, err
	}
	v := toView(inv)
	return &v, nil
}

// Accept creates the invited account and returns its email.
func Accept(d *sql.DB, token, name, password string, locale enums.Locale) (string, error) {
	var email string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		inv, err := usableInvite(tx, token)
		if err != nil {
			return err
		}
		if inv == nil {
			return ErrInviteInvalid
		}

		user, err := accounts.Create(tx, inv.Email, name, &password, inv.Role, locale, "")
		if err != nil {
			return err
		}
		assignments := make([]accounts.TeamAssignment, 0, len(inv.Teams))
		for _, t := range inv.Teams {
			assignments = append(assignments, accounts.TeamAssignment{Team: t.Team, Role: t.Role})
		}
		if err := accounts.JoinTeams(tx, user.ID, assignments); err != nil {
			return err
		}
		if err := authrepo.MarkInviteUsed(tx, inv.ID, now()); err != nil {
			return err
		}
		email = user.Email
		return audit.Log(tx, &user.ID, "invite.accepted", user.Email, "", nil)
	})
	return email, err
}

// RequestReset mails a reset link. It looks the same to the requester
// whether the account exists or not.
func RequestReset(d *sql.DB, email, ip string) error {
	token := crypto.NewToken()
	var address string
	var locale enums.Locale

	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.ByEmail(tx, strings.TrimSpace(email))
		if err != nil {
			return err
		}
		if user == nil || !user.IsActive || user.PasswordHash == "" {
			return nil
		}
		reset := &model.ResetToken{UserID: user.ID, TokenHash: crypto.TokenHash(token), ExpiresAt: now().Add(resetValidity)}
		if err := authrepo.AddReset(tx, reset); err != nil {
			return err
		}
		address, locale = user.Email, user.Locale
		return audit.Log(tx, &user.ID, "reset.requested", "", ip, nil)
	})
	if err != nil || address == "" {
		return err
	}
	return mail.Reset(address, link("reset", token), locale)
}

// AdminResetLink creates a reset link for a user (works without SMTP).
func AdminResetLink(d *sql.DB, who *access.Principal, userID int64) (string, error) {
	if !who.IsAdmin() {
		return "", ErrDenied
	}
	token := crypto.NewToken()
	err := db.WithTx(d, func(tx *sql.Tx) error {
		reset := &model.ResetToken{UserID: userID, TokenHash: crypto.TokenHash(token), ExpiresAt: now().Add(adminResetValidity)}
		if err := authrepo.AddReset(tx, reset); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "reset.admin_link", strconv.FormatInt(userID, 10), "", nil)
	})
	if err != nil {
		return "", err
	}
	return link("reset", token), nil
}

func usableReset(q db.Queryer, token string) (*model.ResetToken, error) {
	r, err := authrepo.ResetByHash(q, crypto.TokenHash(token))
	if err != nil {
		return nil, err
	}
	if r == nil || r.UsedAt != nil || r.ExpiresAt.Before(now()) {
		return nil, nil
	}
	return r, nil
}

// ResetValid reports whether token can still be used.
func ResetValid(d *sql.DB, token string) (bool, error) {
	r, err := usableReset(d, token)
	return r != nil, err
}

// Reset sets a new password, ends every session and notifies the user.
func Reset(d *sql.DB, token, password, ip string) error {
	if err := accounts.CheckPasswordRules(password); err != nil {
		return err
	}
	var userID int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		r, err := usableReset(tx, token)
		if err != nil {
			return err
		}
		if r == nil {
			return ErrResetInvalid
		}
		user, err := users.Get(tx, r.UserID)
		if err != nil {
			return err
		}
		if user == nil {
			return ErrResetInvalid
		}

		hash, err := crypto.HashPassword(password)
		if err != nil {
			return err
		}
		user.PasswordHash = hash
		if err := users.Update(tx, user); err != nil {
			return err
		}
		if err := authrepo.MarkResetUsed(tx, r.ID, now()); err != nil {
			return err
		}
		if err := authrepo.DropSessions(tx, user.ID, nil); err != nil {
			return err
		}
		userID = user.ID
		return audit.Log(tx, &user.ID, "reset.done", "", ip, nil)
	})
	if err != nil {
		return err
	}
	return mail.SecurityNotice(d, userID, mail.PasswordChanged)
}
