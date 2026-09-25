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
	"dashboard/internal/services/hints"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/sources"
)

const connectorRule = "system.connector_down"

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
		settings := sp.Settings
		if settings == nil {
			settings = map[string]any{}
		}
		mine := connectionsOf(conns, sp.ID)

		scopes := map[ownerKey]*scope{{nil}: newScope(sp.ID, nil, settings)}
		for _, conn := range mine {
			n, err := runConnection(ctx, d, conn, owners[conn.ID], scopes, settings, today)
			if err != nil {
				slog.Error("analysis: connection failed", "connection", conn.Name, "err", err)
				continue
			}
			fresh += n
		}
		for _, sc := range scopes {
			n, err := runScope(d, sc, today)
			if err != nil {
				slog.Error("analysis: cross/deadline rules failed", "space", sp.ID, "err", err)
				continue
			}
			fresh += n
		}
	}
	return fresh, nil
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

func runConnection(ctx context.Context, d *sql.DB, conn *model.Connection, users []int64, scopes map[ownerKey]*scope, settings map[string]any, today time.Time) (int, error) {
	fresh := 0
	for _, owner := range owningUsers(conn, users) {
		key := ownerKey{owner}
		sc, ok := scopes[key]
		if !ok {
			sc = newScope(conn.SpaceID, owner, settings)
			scopes[key] = sc
		}

		sourceKey := conn.Service + ".data"
		result, err := svcdata.Get(ctx, d, sourceKey, nil, conn, owner, svcdata.Cached)
		if err != nil {
			if errors.Is(err, svcdata.ErrMissingCredential) {
				continue
			}
			return fresh, err
		}

		var down []rules.Finding
		if !result.Ok() {
			down = []rules.Finding{downFinding(conn, result.Error)}
		}
		n, err := syncHints(d, conn.SpaceID, owner, &conn.ID, []string{connectorRule}, down)
		if err != nil {
			return fresh, err
		}
		fresh += n
		if result.Data == nil {
			continue
		}

		sc.datasets[conn.Service] = result.Data
		sc.options[conn.Service] = conn.Options
		if err := snapshot(d, conn, owner, result.Data, today); err != nil {
			slog.Error("analysis: snapshot failed", "connection", conn.Name, "err", err)
		}
		env := rules.Env{Today: today, Settings: settings, Datasets: sc.datasets, Options: sc.options}
		findings, ids := apply(rules.ForScope(conn.Service), result.Data, env)
		n, err = syncHints(d, conn.SpaceID, owner, &conn.ID, ids, findings)
		if err != nil {
			return fresh, err
		}
		fresh += n
	}
	return fresh, nil
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
