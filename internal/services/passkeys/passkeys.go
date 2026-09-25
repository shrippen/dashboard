// Package passkeys registers WebAuthn credentials and logs in with them.
//
//	register: Begin ──options──► browser (navigator.credentials.create)
//	          Finish ◄─attestation── browser → passkeys row
//	login:    BeginLogin ──ceremony id + options──► browser (.get, discoverable)
//	          FinishLogin ◄─assertion── browser → auth.OpenPasskeySession
//
// Challenges live in memory for a few minutes (one container, like the
// login throttle).
package passkeys

import (
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/model"
	authrepo "dashboard/internal/repos/auth"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	auditsvc "dashboard/internal/services/audit"
	"dashboard/internal/services/auth"
	"dashboard/internal/services/mail"
	"dashboard/internal/services/util"
	"dashboard/internal/settings"
)

const (
	ceremonyTTL = 5 * time.Minute
	maxPending  = 1000 // login begin is public: bound the memory it takes
	rpName      = "dashboard"
	nameMax     = 60
)

var (
	ErrCeremony = errors.New("passkey.expired")
	ErrInvalid  = errors.New("passkey.invalid")
	ErrBusy     = errors.New("login.throttled")
)

// ── Pending ceremonies ──

type ceremony struct {
	session webauthn.SessionData
	userID  int64 // 0 for login
	expires time.Time
}

var (
	pendingMu sync.Mutex
	pending   = map[string]ceremony{}
)

func keep(key string, c ceremony) error {
	pendingMu.Lock()
	defer pendingMu.Unlock()

	now := time.Now()
	for k, old := range pending {
		if now.After(old.expires) {
			delete(pending, k)
		}
	}
	if len(pending) >= maxPending {
		return ErrBusy
	}
	c.expires = now.Add(ceremonyTTL)
	pending[key] = c
	return nil
}

// take returns and forgets a ceremony: each challenge is good once.
func take(key string) (ceremony, bool) {
	pendingMu.Lock()
	defer pendingMu.Unlock()

	c, ok := pending[key]
	delete(pending, key)
	if !ok || time.Now().After(c.expires) {
		return ceremony{}, false
	}
	return c, true
}

func registerKey(userID int64) string {
	return "reg:" + string(userHandle(userID))
}

// ── Relying party and user ──

// relyingParty derives RP id and origin from BASE_URL
// ("https://dash.example.org:8443" → id dash.example.org).
func relyingParty(cfg settings.Settings) (*webauthn.WebAuthn, error) {
	base, err := url.Parse(cfg.BaseURL)
	if err != nil || base.Host == "" {
		return nil, ErrInvalid
	}
	origin := base.Scheme + "://" + base.Host
	return webauthn.New(&webauthn.Config{RPID: base.Hostname(), RPDisplayName: rpName, RPOrigins: []string{origin}})
}

// userHandle is the user id as 8 bytes; it never leaves the RP and
// authenticator, and ids are not secret here.
func userHandle(userID int64) []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(userID))
}

type account struct {
	user  *model.User
	keys  []*model.Passkey
	creds []webauthn.Credential
}

func (a *account) WebAuthnID() []byte                         { return userHandle(a.user.ID) }
func (a *account) WebAuthnName() string                       { return a.user.Email }
func (a *account) WebAuthnDisplayName() string                { return a.user.Name }
func (a *account) WebAuthnCredentials() []webauthn.Credential { return a.creds }

func loadAccount(q db.Queryer, userID int64) (*account, error) {
	user, err := users.Get(q, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, util.ErrNotFound
	}
	keys, err := authrepo.PasskeysOf(q, userID)
	if err != nil {
		return nil, err
	}
	a := &account{user: user, keys: keys}
	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(k.Data), &c); err != nil {
			return nil, err
		}
		a.creds = append(a.creds, c)
	}
	return a, nil
}

func credID(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }

// ── Management ──

// Mine lists the principal's passkeys.
func Mine(d *sql.DB, who *access.Principal) ([]*model.Passkey, error) {
	return authrepo.PasskeysOf(d, who.UserID)
}

// Begin starts registering a passkey; the result is the JSON for
// navigator.credentials.create.
func Begin(d *sql.DB, cfg settings.Settings, who *access.Principal) ([]byte, error) {
	rp, err := relyingParty(cfg)
	if err != nil {
		return nil, err
	}
	acc, err := loadAccount(d, who.UserID)
	if err != nil {
		return nil, err
	}

	var exclude []protocol.CredentialDescriptor
	for _, c := range acc.creds {
		exclude = append(exclude, c.Descriptor())
	}
	options, session, err := rp.BeginRegistration(acc,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementRequired, UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithExclusions(exclude))
	if err != nil {
		return nil, err
	}
	if err := keep(registerKey(who.UserID), ceremony{session: *session, userID: who.UserID}); err != nil {
		return nil, err
	}
	return json.Marshal(options)
}

// Finish verifies the browser's attestation and stores the passkey.
func Finish(d *sql.DB, cfg settings.Settings, who *access.Principal, name string, body io.Reader, ip string) error {
	c, ok := take(registerKey(who.UserID))
	if !ok {
		return ErrCeremony
	}
	rp, err := relyingParty(cfg)
	if err != nil {
		return err
	}
	parsed, err := protocol.ParseCredentialCreationResponseBody(body)
	if err != nil {
		return ErrInvalid
	}

	err = db.WithTx(d, func(tx *sql.Tx) error {
		acc, err := loadAccount(tx, who.UserID)
		if err != nil {
			return err
		}
		cred, err := rp.CreateCredential(acc, c.session, parsed)
		if err != nil {
			return ErrInvalid
		}
		data, err := json.Marshal(cred)
		if err != nil {
			return err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			name = "Passkey"
		}
		if len(name) > nameMax {
			name = name[:nameMax]
		}
		key := &model.Passkey{UserID: who.UserID, CredID: credID(cred.ID), Name: name, Data: string(data),
			CreatedAt: time.Now().UTC()}
		if err := authrepo.AddPasskey(tx, key); err != nil {
			return err
		}
		return auditsvc.Log(tx, &who.UserID, "passkey.added", name, ip, nil)
	})
	if err != nil {
		return err
	}
	_ = mail.SecurityNotice(d, who.UserID, mail.PasskeyAdded)
	return nil
}

// Remove deletes one of the principal's passkeys.
func Remove(d *sql.DB, who *access.Principal, passkeyID int64, ip string) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		key, err := authrepo.Passkey(tx, passkeyID)
		if err != nil {
			return err
		}
		if key == nil || key.UserID != who.UserID {
			return util.ErrNotFound
		}
		if err := authrepo.RemovePasskey(tx, passkeyID); err != nil {
			return err
		}
		return auditsvc.Log(tx, &who.UserID, "passkey.removed", key.Name, ip, nil)
	})
}

// ── Login ──

// BeginLogin starts a discoverable login: the browser offers every passkey
// it holds for this site. Returns the ceremony id and the JSON for
// navigator.credentials.get.
func BeginLogin(cfg settings.Settings) (string, []byte, error) {
	rp, err := relyingParty(cfg)
	if err != nil {
		return "", nil, err
	}
	options, session, err := rp.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return "", nil, err
	}
	id := crypto.NewToken()
	if err := keep(id, ceremony{session: *session}); err != nil {
		return "", nil, err
	}
	data, err := json.Marshal(options)
	return id, data, err
}

// FinishLogin verifies an assertion and opens a session; it returns the
// session cookie value.
func FinishLogin(d *sql.DB, cfg settings.Settings, ceremonyID string, body io.Reader, ip, agent string) (string, error) {
	c, ok := take(ceremonyID)
	if !ok {
		return "", ErrCeremony
	}
	rp, err := relyingParty(cfg)
	if err != nil {
		return "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBody(body)
	if err != nil {
		return "", ErrInvalid
	}

	var acc *account
	lookup := func(rawID, handle []byte) (webauthn.User, error) {
		if len(handle) != len(userHandle(0)) {
			return nil, ErrInvalid
		}
		a, err := loadAccount(d, int64(binary.BigEndian.Uint64(handle)))
		if err != nil {
			return nil, err
		}
		acc = a
		return a, nil
	}
	_, cred, err := rp.ValidatePasskeyLogin(lookup, c.session, parsed)
	if err != nil || cred.Authenticator.CloneWarning {
		return "", ErrInvalid
	}

	if err := touch(d, acc, cred); err != nil {
		return "", err
	}
	return auth.OpenPasskeySession(d, cfg, acc.user.ID, ip, agent)
}

// touch stores the new sign count so a cloned authenticator is noticed.
func touch(d *sql.DB, acc *account, cred *webauthn.Credential) error {
	data, err := json.Marshal(cred)
	if err != nil {
		return err
	}
	for _, k := range acc.keys {
		if k.CredID == credID(cred.ID) {
			return authrepo.TouchPasskey(d, k.ID, string(data), time.Now().UTC())
		}
	}
	return ErrInvalid
}
