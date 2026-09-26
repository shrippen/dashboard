package sources

// FreshRSS (feeds), Gitea (code) and Borg Backup Server (backups).

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"andon/internal/drivers/services"
	"andon/internal/enums"
)

const (
	giteaPage       = 50
	giteaMaxPages   = 5
	giteaActiveDays = 30
	usecPerSecond   = 1_000_000
	readingList     = "user/-/state/com.google/reading-list"
)

// ── FreshRSS ──

type Feed struct {
	ID, Title, Category string
	Unread              int
	Newest              time.Time // zero if unknown
}

type FreshRSSDataset struct {
	URL    string
	Unread int
	Feeds  []Feed
}

type FreshRSSData struct{}

func (FreshRSSData) Key() string                { return "freshrss.data" }
func (FreshRSSData) TTL() time.Duration         { return opsTTL }
func (FreshRSSData) Service() enums.ServiceType { return enums.ServiceFreshRSS }

func (FreshRSSData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoFreshRSS(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.FreshRSSApi{URL: sctx.URL, Secret: secret, Verify: sctx.VerifyTLS}
	auth, err := api.Open(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	subs, err := api.Get(ctx, auth, "subscription/list")
	if err != nil {
		return nil, fetchError(err)
	}
	counts, err := api.Get(ctx, auth, "unread-count")
	if err != nil {
		return nil, fetchError(err)
	}
	return parseFreshRSS(sctx.URL, subs, counts), nil
}

func parseFreshRSS(base string, subs, counts any) *FreshRSSDataset {
	data := &FreshRSSDataset{URL: base}
	byID := map[string]*Feed{}
	for _, raw := range asList(asMap(subs)["subscriptions"]) {
		s := asMap(raw)
		f := &Feed{ID: asStr(s["id"]), Title: asStr(s["title"])}
		if cats := asList(s["categories"]); len(cats) > 0 {
			f.Category = asStr(asMap(cats[0])["label"])
		}
		byID[f.ID] = f
	}
	for _, raw := range asList(asMap(counts)["unreadcounts"]) {
		c := asMap(raw)
		id, n := asStr(c["id"]), int(asFloat(c["count"]))
		if id == readingList {
			data.Unread = n
			continue
		}
		f, ok := byID[id]
		if !ok {
			continue
		}
		f.Unread = n
		if usec, _ := strconv.ParseInt(asStr(c["newestItemTimestampUsec"]), 10, 64); usec > 0 {
			f.Newest = time.Unix(usec/usecPerSecond, 0).UTC()
		}
	}
	for _, f := range byID {
		data.Feeds = append(data.Feeds, *f)
	}
	// Servers without the reading-list total: sum the feeds.
	if data.Unread == 0 {
		for _, f := range data.Feeds {
			data.Unread += f.Unread
		}
	}
	sort.Slice(data.Feeds, func(i, j int) bool { return data.Feeds[i].Title < data.Feeds[j].Title })
	return data
}

// ── Gitea ──

// Issue is an open issue or pull request that concerns the user.
type Issue struct {
	Repo, Title, URL string
	Number           int64
	Pull             bool
	Due, Updated     time.Time // Due zero if none
}

type Repo struct {
	Name, URL      string
	Mirror         bool
	MirrorUpdated  time.Time
	Updated        time.Time
	FailedWorkflow string // latest Actions run failed: its name
}

type GiteaDataset struct {
	URL           string
	User          string
	Notifications int
	Assigned      []Issue
	Reviews       []Issue // PRs waiting for the user's review
	Repos         []Repo
}

type GiteaData struct{}

func (GiteaData) Key() string                { return "gitea.data" }
func (GiteaData) TTL() time.Duration         { return opsTTL }
func (GiteaData) Service() enums.ServiceType { return enums.ServiceGitea }

func (GiteaData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoGitea(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadGitea(ctx, services.GiteaApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, sctx.URL, time.Now())
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func parseTime(v any) time.Time {
	t, _ := time.Parse(time.RFC3339, asStr(v))
	return t.UTC()
}

func giteaIssues(raw any) []Issue {
	var out []Issue
	for _, item := range asList(raw) {
		m := asMap(item)
		out = append(out, Issue{
			Repo: asStr(asMap(m["repository"])["full_name"]), Title: asStr(m["title"]), URL: asStr(m["html_url"]),
			Number: asInt64(m["number"]), Pull: m["pull_request"] != nil,
			Due: parseTime(m["due_date"]), Updated: parseTime(m["updated_at"]),
		})
	}
	return out
}

func loadGitea(ctx context.Context, api services.GiteaApi, base string, now time.Time) (*GiteaDataset, error) {
	me, err := api.Get(ctx, "user", nil)
	if err != nil {
		return nil, err
	}
	data := &GiteaDataset{URL: base, User: asStr(asMap(me)["login"])}

	if n, err := api.Get(ctx, "notifications/new", nil); err == nil {
		data.Notifications = int(asFloat(asMap(n)["new"]))
	}
	limit := strconv.Itoa(giteaPage)
	assigned, err := api.Get(ctx, "repos/issues/search", url.Values{"type": {"assigned"}, "state": {"open"}, "limit": {limit}})
	if err != nil {
		return nil, err
	}
	data.Assigned = giteaIssues(assigned)
	// review_requested exists since Gitea 1.19; older servers just lack it.
	if reviews, err := api.Get(ctx, "repos/issues/search", url.Values{"review_requested": {"true"}, "type": {"pulls"}, "state": {"open"}, "limit": {limit}}); err == nil {
		data.Reviews = giteaIssues(reviews)
	}

	for page := 1; page <= giteaMaxPages; page++ {
		repos, err := api.Get(ctx, "user/repos", url.Values{"limit": {limit}, "page": {strconv.Itoa(page)}})
		if err != nil {
			return nil, err
		}
		list := asList(repos)
		for _, raw := range list {
			m := asMap(raw)
			if asBool(m["archived"]) {
				continue
			}
			repo := Repo{Name: asStr(m["full_name"]), URL: asStr(m["html_url"]), Mirror: asBool(m["mirror"]),
				MirrorUpdated: parseTime(m["mirror_updated"]), Updated: parseTime(m["updated_at"])}
			if now.Sub(repo.Updated) < giteaActiveDays*24*time.Hour {
				repo.FailedWorkflow = latestFailedRun(ctx, api, repo.Name)
			}
			data.Repos = append(data.Repos, repo)
		}
		if len(list) < giteaPage {
			break
		}
	}
	return data, nil
}

// latestFailedRun returns the latest Actions run's name when it failed.
// Servers without Actions answer 404; that counts as none.
func latestFailedRun(ctx context.Context, api services.GiteaApi, repo string) string {
	runs, err := api.Get(ctx, "repos/"+repo+"/actions/runs", url.Values{"limit": {"1"}})
	if err != nil {
		return ""
	}
	list := asList(asMap(runs)["workflow_runs"])
	if len(list) == 0 {
		return ""
	}
	run := asMap(list[0])
	if asStr(run["conclusion"]) != "failure" && asStr(run["status"]) != "failure" {
		return ""
	}
	name := asStr(run["display_title"])
	if name == "" {
		name = asStr(run["path"])
	}
	return strings.TrimSpace(name)
}

// ── Borg Backup Server ──

type BorgClient struct {
	Name, Status string // online, offline, error, setup
	LastSeen     time.Time
	LastBackup   time.Time // zero when the server does not report it
}

type BorgDataset struct {
	URL            string
	Clients        []BorgClient
	Failed24h      int
	Completed24h   int
	Running        int
	UsedBytes      float64
	TotalBytes     float64
	LastBackup     time.Time
	AgentsOutdated int
	ServerUpdate   bool
}

type BorgData struct{}

func (BorgData) Key() string                { return "borgbackup.data" }
func (BorgData) TTL() time.Duration         { return opsTTL }
func (BorgData) Service() enums.ServiceType { return enums.ServiceBorgBackup }

// borgTime reads "2026-09-25 03:00:00" (server local time) or RFC 3339.
func borgTime(v any) time.Time {
	s := asStr(v)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC()
	}
	t, _ := time.ParseInLocation(time.DateTime, s, time.Local)
	return t.UTC()
}

func (BorgData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoBorg(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.BorgApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}
	dash, err := api.Get(ctx, "dashboard")
	if err != nil {
		return nil, fetchError(err)
	}
	clients, err := api.Get(ctx, "clients")
	if err != nil {
		return nil, fetchError(err)
	}
	return parseBorg(sctx.URL, dash, clients), nil
}

func parseBorg(base string, dash, clients any) *BorgDataset {
	d := asMap(dash)
	jobs, storage := asMap(d["jobs"]), asMap(d["storage"])
	updates := asMap(d["updates"])
	data := &BorgDataset{
		URL: base, Failed24h: int(asFloat(jobs["failed_24h"])), Completed24h: int(asFloat(jobs["completed_24h"])),
		Running: int(asFloat(jobs["running"])), UsedBytes: asFloat(storage["used_bytes"]), TotalBytes: asFloat(storage["total_bytes"]),
		LastBackup: borgTime(asMap(d["archives"])["last_backup_at"]), AgentsOutdated: int(asFloat(updates["agents_outdated"])),
		ServerUpdate: asBool(updates["server_available"]),
	}
	for _, raw := range asList(asMap(clients)["clients"]) {
		c := asMap(raw)
		data.Clients = append(data.Clients, BorgClient{Name: asStr(c["name"]), Status: asStr(c["status"]), LastSeen: borgTime(c["last_heartbeat"]),
			LastBackup: borgTime(c["last_backup_at"])})
	}
	return data
}

func init() {
	Register(FreshRSSData{})
	Register(testOf{FreshRSSData{}, func(d any) map[string]any { return map[string]any{"feeds": len(d.(*FreshRSSDataset).Feeds)} }})
	Register(GiteaData{})
	Register(testOf{GiteaData{}, func(d any) map[string]any { return map[string]any{"user": d.(*GiteaDataset).User} }})
	Register(BorgData{})
	Register(testOf{BorgData{}, func(d any) map[string]any { return map[string]any{"clients": len(d.(*BorgDataset).Clients)} }})
}
