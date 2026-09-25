// Package analysis runs all rules and reconciles hints (scheduler job,
// every 5 minutes).
//
//	for each space
//	  for each owner (nil = shared data, user id = personal credentials)
//	    datasets  ← <service>.data per connection (cached, 10 min)
//	    service rules  → hints (per connection)
//	    cross + deadline rules → hints (per space and owner)
package analysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/rules"
	"dashboard/internal/services/access"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/linkstatus"
	"dashboard/internal/services/scheduler"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/sources"
)

// JobName is the scheduler job that runs RunAll: it fetches every
// integration; page views only read its results.
const JobName = "analysis"

const (
	connectorRule = "system.connector_down"
	linkType      = "link"
)

// scope is one space's datasets for one credential owner.
type scope struct {
	spaceID  int64
	owner    *int64
	settings map[string]any
	datasets map[string]any
	options  map[string]map[string]any
}

func newScope(spaceID int64, owner *int64, settings map[string]any) *scope {
	return &scope{spaceID: spaceID, owner: owner, settings: settings, datasets: map[string]any{}, options: map[string]map[string]any{}}
}

// RunAll runs every rule against every space's connections and reconciles
// hints. Returns the number of hints that are new or reopened (used to
// decide whether to notify).
func RunAll(ctx context.Context, d *sql.DB, today time.Time) (int, error) {
	var spaces []*model.Space
	var conns []*model.Connection
	owners := map[int64][]int64{}

	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		spaces, err = content.AllSpaces(tx)
		if err != nil {
			return err
		}
		conns, err = content.AllConnections(tx)
		if err != nil {
			return err
		}
		for _, c := range conns {
			creds, err := content.Credentials(tx, c.ID)
			if err != nil {
				return err
			}
			ids := make([]int64, len(creds))
			for i, cr := range creds {
				ids[i] = cr.UserID
			}
			owners[c.ID] = ids
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	fresh := 0
	for _, sp := range spaces {
		n, err := runSpace(ctx, d, sp, connectionsOf(conns, sp.ID), owners, today)
		if err != nil {
			slog.Error("analysis: space failed", "space", sp.ID, "err", err)
		}
		fresh += n
	}
	return fresh, nil
}

// run is one fetched connection for one credential owner.
type run struct {
	conn   *model.Connection
	owner  *int64
	result svcdata.Result
}

// runSpace fetches every connection first, then evaluates: rules see all
// datasets and failures of the space, so one outage becomes one hint.
func runSpace(ctx context.Context, d *sql.DB, sp *model.Space, mine []*model.Connection, owners map[int64][]int64, today time.Time) (int, error) {
	settings := sp.Settings
	if settings == nil {
		settings = map[string]any{}
	}
	links, err := spaceLinks(d, sp.ID)
	if err != nil {
		slog.Error("analysis: links failed", "space", sp.ID, "err", err)
	}

	scopes := map[ownerKey]*scope{{nil}: newScope(sp.ID, nil, settings)}
	scopeOf := func(owner *int64) *scope {
		key := ownerKey{owner}
		if _, ok := scopes[key]; !ok {
			scopes[key] = newScope(sp.ID, owner, settings)
		}
		return scopes[key]
	}

	// Certificate checks go last: they may pick up hosts found by others.
	var runs []run
	for _, conn := range certsLast(mine) {
		for _, owner := range owningUsers(conn, owners[conn.ID]) {
			sc := scopeOf(owner)
			target := conn
			if conn.Service == string(enums.ServiceCerts) {
				target = withAutoHosts(conn, links, sc.datasets)
			}
			result, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceType(conn.Service)), nil, target, owner, svcdata.Cached)
			if errors.Is(err, svcdata.ErrMissingCredential) {
				continue
			}
			if err != nil {
				slog.Error("analysis: connection failed", "connection", conn.Name, "err", err)
				continue
			}
			runs = append(runs, run{conn: conn, owner: owner, result: result})
			if result.Data != nil {
				sc.datasets[conn.Service] = result.Data
				sc.options[conn.Service] = conn.Options
			}
			if !result.Ok() {
				failed, _ := sc.datasets[rules.FailedDataset].([]rules.Failed)
				sc.datasets[rules.FailedDataset] = append(failed, rules.Failed{Service: conn.Service, Name: conn.Name, Host: rules.HostOf(conn.URL)})
			}
		}
	}
	for _, sc := range scopes {
		sc.datasets[rules.LinksDataset] = links
	}
	if err := addSecrets(d, mine, owners, scopeOf); err != nil {
		slog.Error("analysis: secrets failed", "space", sp.ID, "err", err)
	}
	for _, sc := range scopes {
		history, err := recordHistory(d, sc, time.Now().UTC())
		if err != nil {
			slog.Error("analysis: history failed", "space", sp.ID, "err", err)
			continue
		}
		sc.datasets[metrics.HistoryDataset] = history
	}

	fresh := 0
	for _, r := range runs {
		n, err := evaluate(d, r, scopeOf(r.owner), settings, today)
		if err != nil {
			return fresh, err
		}
		fresh += n
	}
	for _, sc := range scopes {
		n, err := runScope(d, sc, today)
		if err != nil {
			return fresh, err
		}
		fresh += n
	}
	return fresh, nil
}

// addSecrets lists each scope's stored secrets for the token rule: the
// shared token, or the owner's own personal one.
func addSecrets(d *sql.DB, conns []*model.Connection, owners map[int64][]int64, scopeOf func(*int64) *scope) error {
	for _, conn := range conns {
		for _, owner := range owningUsers(conn, owners[conn.ID]) {
			c := rules.Conn{Name: conn.Name, SecretAt: conn.SecretAt, Expires: conn.SecretExpires}
			if owner != nil {
				cred, err := content.Credential(d, conn.ID, *owner)
				if err != nil {
					return err
				}
				c.SecretAt = time.Time{}
				if cred != nil {
					c.SecretAt = cred.SecretAt
				}
			}
			sc := scopeOf(owner)
			list, _ := sc.datasets[rules.ConnsDataset].([]rules.Conn)
			sc.datasets[rules.ConnsDataset] = append(list, c)
		}
	}
	return nil
}

// certsLast orders certificate connections after all others.
func certsLast(conns []*model.Connection) []*model.Connection {
	out := make([]*model.Connection, 0, len(conns))
	var certs []*model.Connection
	for _, c := range conns {
		if c.Service == string(enums.ServiceCerts) {
			certs = append(certs, c)
			continue
		}
		out = append(out, c)
	}
	return append(out, certs...)
}

// evaluate turns one fetched connection into hints.
func evaluate(d *sql.DB, r run, sc *scope, settings map[string]any, today time.Time) (int, error) {
	env := rules.Env{Today: today, Settings: settings, Datasets: sc.datasets, Options: sc.options}
	outages := rules.Outages(env)

	// A connection on a host that is down as a whole is part of the outage hint.
	var down []rules.Finding
	if _, inOutage := outages[rules.HostOf(r.conn.URL)]; !r.result.Ok() && !inOutage {
		down = []rules.Finding{downFinding(r.conn, r.result.Error)}
	}
	fresh, err := syncHints(d, r.conn.SpaceID, r.owner, &r.conn.ID, []string{connectorRule}, down)
	if err != nil || r.result.Data == nil {
		return fresh, err
	}

	if err := snapshot(d, r.conn, r.owner, r.result.Data, today); err != nil {
		slog.Error("analysis: snapshot failed", "connection", r.conn.Name, "err", err)
	}
	findings, ids := apply(rules.ForScope(r.conn.Service), r.result.Data, env)
	kept := findings[:0]
	for _, f := range findings {
		if !rules.Suppressed(f, env, outages) {
			kept = append(kept, f)
		}
	}
	n, err := syncHints(d, r.conn.SpaceID, r.owner, &r.conn.ID, ids, kept)
	return fresh + n, err
}

// spaceLinks returns the space's link tiles, for rules that compare them
// with bookmarks (Linkwarden) or monitors (Uptime Kuma).
func spaceLinks(d *sql.DB, spaceID int64) ([]rules.Link, error) {
	widgets, err := content.Widgets(d, []int64{spaceID})
	if err != nil {
		return nil, err
	}
	clicks, err := data.LastClicks(d)
	if err != nil {
		return nil, err
	}
	var links []rules.Link
	for _, w := range widgets {
		target, _ := w.Config["url"].(string)
		if w.Type == linkType && target != "" {
			links = append(links, rules.Link{Title: w.Title, URL: target, DownDays: linkstatus.DownDays(d, w.ID, time.Now().UTC()),
				LastClick: clicks[w.ID]})
		}
	}
	return links, nil
}

// ownerKey wraps *int64 so it can key a map (nil vs non-nil user ids are
// distinct owners; two nils are the same "shared" owner).
type ownerKey struct{ id *int64 }

func connectionsOf(conns []*model.Connection, spaceID int64) []*model.Connection {
	var out []*model.Connection
	for _, c := range conns {
		if c.SpaceID == spaceID {
			out = append(out, c)
		}
	}
	return out
}

func owningUsers(conn *model.Connection, users []int64) []*int64 {
	if conn.CredentialMode != enums.CredentialPersonal {
		return []*int64{nil}
	}
	out := make([]*int64, len(users))
	for i := range users {
		out[i] = &users[i]
	}
	return out
}

// snapshot stores one value per metric and day, for the trend widget.
func snapshot(d *sql.DB, conn *model.Connection, owner *int64, dataset any, today time.Time) error {
	values := map[string]float64{}
	switch conn.Service {
	case "invoiceninja":
		stats := metrics.NinjaSummaryOf(dataset.(*sources.NinjaDataset), today, "", "")
		values["revenue_ytd"] = stats.RevenueYTD
		values["open_amount"] = stats.OpenAmount
	case "kimai":
		stats := metrics.KimaiSummaryOf(dataset.(*sources.KimaiDataset), today, 0)
		values["month_min"] = float64(stats.MonthMin)
	}
	if len(values) == 0 {
		return nil
	}

	ownerID := int64(0)
	if owner != nil {
		ownerID = *owner
	}
	scope := fmt.Sprintf("%d:%d", conn.ID, ownerID)
	day := today.Format("2006-01-02")
	return db.WithTx(d, func(tx *sql.Tx) error {
		for metric, value := range values {
			if err := data.PutPoint(tx, scope, metric, day, value); err != nil {
				return err
			}
		}
		return nil
	})
}

func runScope(d *sql.DB, sc *scope, today time.Time) (int, error) {
	env := rules.Env{Today: today, Settings: sc.settings, Datasets: sc.datasets, Options: sc.options}
	specs := append(rules.ForScope(rules.Cross), rules.ForScope(rules.Deadlines)...)
	findings, ids := apply(specs, nil, env)
	return syncHints(d, sc.spaceID, sc.owner, nil, ids, findings)
}

func apply(specs []rules.Spec, dataset any, env rules.Env) ([]rules.Finding, []string) {
	var findings []rules.Finding
	ids := make([]string, 0, len(specs))
	for _, spec := range specs {
		cfg := rules.Config(spec, env.Settings)
		ids = append(ids, spec.ID)
		if enabled, ok := cfg[rules.Enabled].(bool); ok && !enabled {
			continue
		}
		findings = append(findings, safeRun(spec, dataset, cfg, env)...)
	}
	return findings, ids
}

// safeRun isolates one rule's panic (a broken rule must not stop the others
// or take the analysis job down).
func safeRun(spec rules.Spec, dataset any, cfg map[string]any, env rules.Env) (out []rules.Finding) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("analysis: rule panicked", "rule", spec.ID, "recover", r)
			out = nil
		}
	}()
	return spec.Run(dataset, cfg, env)
}

func downFinding(conn *model.Connection, errMsg string) rules.Finding {
	if errMsg == "" {
		errMsg = "?"
	}
	return rules.Finding{
		Fingerprint: fmt.Sprintf("down:%d", conn.ID), Rule: connectorRule, Severity: enums.SeverityWarn,
		Message: "system.connector_down", Params: map[string]any{"name": conn.Name, "error": errMsg},
		Sources: []string{conn.Service},
	}
}

func syncHints(d *sql.DB, spaceID int64, owner *int64, connID *int64, ruleIDs []string, findings []rules.Finding) (int, error) {
	var n int
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		n, err = hints.Sync(tx, spaceID, owner, connID, ruleIDs, findings)
		return err
	})
	return n, err
}

// ErrNotScheduled means no scheduler runs the analysis (e.g. disabled).
var ErrNotScheduled = errors.New("analysis.not_scheduled")

// RequestRun asks the scheduler for a run now. Admins only.
func RequestRun(who *access.Principal) error {
	if !who.IsAdmin() {
		return access.ErrDenied
	}
	if !scheduler.Trigger(JobName) {
		return ErrNotScheduled
	}
	return nil
}

// LastRun reports the latest background run, false before the first.
func LastRun() (scheduler.Run, bool) {
	return scheduler.LastRun(JobName)
}
