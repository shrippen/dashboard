// Package seed fills a fresh instance: the demo instance (ANDON_DEMO)
// and an optional seed.yml (SEED_FILE) imported into the instance space.
package seed

import (
	"context"
	"database/sql"
	_ "embed"
	"log/slog"
	"os"
	"time"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/analysis"
	"andon/internal/services/porting"
)

// Demo accounts (made-up data, connections use demo:// URLs).
const (
	DemoAdmin    = "admin@demo.local"
	DemoUser     = "alex@demo.local"
	DemoPassword = "demo-password-1"
	demoTeam     = "IT"
)

//go:embed demo/instance.yml
var demoInstance string

//go:embed demo/personal.yml
var demoPersonal string

// Demo creates demo users and boards once (only while no user exists).
func Demo(ctx context.Context, d *sql.DB) error {
	var adminID, userID int64
	created := false
	err := db.WithTx(d, func(tx *sql.Tx) error {
		n, err := users.Count(tx)
		if err != nil || n > 0 {
			return err
		}
		password := DemoPassword
		admin, err := accounts.Create(tx, DemoAdmin, "Admin", &password, enums.RoleAdmin, enums.LocaleDE, "")
		if err != nil {
			return err
		}
		admin.IsBreakglass = true
		if err := users.Update(tx, admin); err != nil {
			return err
		}
		user, err := accounts.Create(tx, DemoUser, "Alex", &password, enums.RoleUser, enums.LocaleDE, "")
		if err != nil {
			return err
		}
		if err := accounts.JoinTeams(tx, user.ID, []accounts.TeamAssignment{{Team: demoTeam, Role: enums.TeamEditor}}); err != nil {
			return err
		}
		if err := accounts.JoinTeams(tx, admin.ID, []accounts.TeamAssignment{{Team: demoTeam, Role: enums.TeamOwner}}); err != nil {
			return err
		}
		adminID, userID, created = admin.ID, user.ID, true
		return nil
	})
	if err != nil || !created {
		return err
	}

	if err := importInto(d, adminID, instanceSpace, demoInstance); err != nil {
		return err
	}
	if err := importInto(d, userID, personalSpace, demoPersonal); err != nil {
		return err
	}
	if _, err := analysis.RunAll(ctx, d, time.Now().UTC()); err != nil {
		return err
	}
	slog.Warn("DEMO MODE", "admin", DemoAdmin, "user", DemoUser, "password", DemoPassword)
	return nil
}

type spaceOf func(q db.Queryer, who *access.Principal) (*model.Space, error)

func instanceSpace(q db.Queryer, _ *access.Principal) (*model.Space, error) {
	return content.InstanceSpace(q)
}

func personalSpace(q db.Queryer, who *access.Principal) (*model.Space, error) {
	return content.PersonalSpace(q, who.UserID)
}

func importInto(d *sql.DB, userID int64, target spaceOf, text string) error {
	who, err := access.Load(d, userID)
	if err != nil {
		return err
	}
	space, err := target(d, who)
	if err != nil || space == nil {
		return err
	}
	report, err := porting.ImportSpace(d, who, space.ID, text, porting.Merge)
	if err != nil {
		return err
	}
	if len(report.Skipped) > 0 {
		slog.Warn("demo import skipped", "items", report.Skipped)
	}
	return nil
}

// FromFile imports seed.yml into the instance space once (while it has no
// boards), as the first admin.
func FromFile(d *sql.DB, path string) error {
	text, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	space, err := content.InstanceSpace(d)
	if err != nil || space == nil {
		return err
	}
	existing, err := content.Boards(d, []int64{space.ID})
	if err != nil || len(existing) > 0 {
		return err
	}

	all, err := users.All(d)
	if err != nil {
		return err
	}
	for _, u := range all {
		if u.Role != enums.RoleAdmin {
			continue
		}
		who, err := access.Load(d, u.ID)
		if err != nil {
			return err
		}
		report, err := porting.ImportSpace(d, who, space.ID, string(text), porting.Merge)
		if err != nil {
			return err
		}
		slog.Info("seed imported", "boards", report.Boards, "widgets", report.Widgets)
		return nil
	}
	slog.Warn("seed.yml waits for the first admin")
	return nil
}
