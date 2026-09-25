package sources

// Media servers and their download helpers:
//
//	mediaserver  Jellyfin (X-Emby-Token) or Plex (X-Plex-Token)   streams, library, updates
//	arr          Sonarr or Radarr (X-Api-Key, detected)           health, queue, missing, upcoming

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	mediaPlex        = "plex"
	mediaJellyfin    = "jellyfin"
	activeSessionsS  = "960"
	arrQueuePage     = "50"
	arrUpcomingDays  = 7
	arrWarning       = "warning"
	sonarrApp        = "Sonarr"
	plexMovieSection = "movie"
	plexShowSection  = "show"
)

// ── Jellyfin / Plex ──

// Stream is one running playback.
type Stream struct {
	User, Title string
}

type MediaServerDataset struct {
	URL            string
	Kind           string
	Version        string
	Update         bool
	Streams        []Stream
	Movies, Series int
	Episodes       int
}

type MediaServerData struct{}

func (MediaServerData) Key() string                { return "mediaserver.data" }
func (MediaServerData) TTL() time.Duration         { return time.Minute }
func (MediaServerData) Service() enums.ServiceType { return enums.ServiceMediaServer }

func (MediaServerData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoMediaServer(), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	var data *MediaServerDataset
	if asStr(sctx.Options["kind"]) == mediaPlex {
		data, err = plex(ctx, services.HeaderApi(sctx.URL, "X-Plex-Token", secret, sctx.VerifyTLS))
	} else {
		data, err = jellyfin(ctx, services.HeaderApi(sctx.URL, "X-Emby-Token", secret, sctx.VerifyTLS))
	}
	if err != nil {
		return nil, fetchError(err)
	}
	data.URL = sctx.URL
	return data, nil
}

func jellyfin(ctx context.Context, api services.KeyedApi) (*MediaServerDataset, error) {
	info, err := api.Get(ctx, "System/Info", nil)
	if err != nil {
		return nil, err
	}
	counts, err := api.Get(ctx, "Items/Counts", nil)
	if err != nil {
		return nil, err
	}
	sessions, err := api.Get(ctx, "Sessions", url.Values{"activeWithinSeconds": {activeSessionsS}})
	if err != nil {
		return nil, err
	}
	i, c := asMap(info), asMap(counts)
	data := &MediaServerDataset{Kind: mediaJellyfin, Version: asStr(i["Version"]), Update: asBool(i["HasUpdateAvailable"]),
		Movies: int(asFloat(c["MovieCount"])), Series: int(asFloat(c["SeriesCount"])), Episodes: int(asFloat(c["EpisodeCount"]))}
	for _, raw := range asList(sessions) {
		s := asMap(raw)
		item := asMap(s["NowPlayingItem"])
		if item == nil {
			continue
		}
		data.Streams = append(data.Streams, Stream{User: asStr(s["UserName"]), Title: firstStr(asStr(item["SeriesName"]), asStr(item["Name"]))})
	}
	return data, nil
}

func plex(ctx context.Context, api services.KeyedApi) (*MediaServerDataset, error) {
	identity, err := api.Get(ctx, "identity", nil)
	if err != nil {
		return nil, err
	}
	sessions, err := api.Get(ctx, "status/sessions", nil)
	if err != nil {
		return nil, err
	}
	sections, err := api.Get(ctx, "library/sections", nil)
	if err != nil {
		return nil, err
	}
	data := &MediaServerDataset{Kind: mediaPlex, Version: asStr(asMap(asMap(identity)["MediaContainer"])["version"])}
	for _, raw := range asList(asMap(asMap(sessions)["MediaContainer"])["Metadata"]) {
		m := asMap(raw)
		data.Streams = append(data.Streams, Stream{User: asStr(asMap(m["User"])["title"]), Title: firstStr(asStr(m["grandparentTitle"]), asStr(m["title"]))})
	}
	for _, raw := range asList(asMap(asMap(sections)["MediaContainer"])["Directory"]) {
		dir := asMap(raw)
		kind := asStr(dir["type"])
		if kind != plexMovieSection && kind != plexShowSection {
			continue
		}
		// Size 0 asks only for the total.
		page, err := api.Get(ctx, "library/sections/"+url.PathEscape(asStr(dir["key"]))+"/all",
			url.Values{"X-Plex-Container-Start": {"0"}, "X-Plex-Container-Size": {"0"}})
		if err != nil {
			return nil, err
		}
		total := int(asFloat(asMap(asMap(page)["MediaContainer"])["totalSize"]))
		if kind == plexMovieSection {
			data.Movies += total
		} else {
			data.Series += total
		}
	}
	return data, nil
}

// ── Sonarr / Radarr ──

// ArrHealth is one health check message.
type ArrHealth struct {
	Level, Message string
}

// ArrItem is one upcoming episode or movie.
type ArrItem struct {
	Title string
	At    time.Time
}

type ArrDataset struct {
	URL      string
	App      string
	Version  string
	Health   []ArrHealth
	Queue    int
	Stuck    []string // queue items with a warning or error
	Missing  int
	Upcoming []ArrItem
}

type ArrData struct{}

func (ArrData) Key() string                { return "arr.data" }
func (ArrData) TTL() time.Duration         { return opsTTL }
func (ArrData) Service() enums.ServiceType { return enums.ServiceArr }

func (ArrData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoArr(time.Now().UTC()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadArr(ctx, services.HeaderApi(sctx.URL, "X-Api-Key", secret, sctx.VerifyTLS), time.Now().UTC())
	if err != nil {
		return nil, fetchError(err)
	}
	data.URL = sctx.URL
	return data, nil
}

func loadArr(ctx context.Context, api services.KeyedApi, now time.Time) (*ArrDataset, error) {
	status, err := api.Get(ctx, "api/v3/system/status", nil)
	if err != nil {
		return nil, err
	}
	health, err := api.Get(ctx, "api/v3/health", nil)
	if err != nil {
		return nil, err
	}
	queue, err := api.Get(ctx, "api/v3/queue", url.Values{"pageSize": {arrQueuePage}})
	if err != nil {
		return nil, err
	}
	data := &ArrDataset{App: asStr(asMap(status)["appName"]), Version: asStr(asMap(status)["version"]),
		Queue: int(asFloat(asMap(queue)["totalRecords"]))}
	for _, raw := range asList(health) {
		h := asMap(raw)
		data.Health = append(data.Health, ArrHealth{Level: asStr(h["type"]), Message: asStr(h["message"])})
	}
	for _, raw := range asList(asMap(queue)["records"]) {
		r := asMap(raw)
		if state := asStr(r["trackedDownloadStatus"]); state == arrWarning || state == "error" {
			data.Stuck = append(data.Stuck, asStr(r["title"]))
		}
	}
	if missing, err := api.Get(ctx, "api/v3/wanted/missing", url.Values{"pageSize": {"1"}, "monitored": {"true"}}); err == nil {
		data.Missing = int(asFloat(asMap(missing)["totalRecords"]))
	}

	params := url.Values{"start": {now.Format(time.DateOnly)}, "end": {now.AddDate(0, 0, arrUpcomingDays).Format(time.DateOnly)}, "includeSeries": {"true"}}
	calendar, err := api.Get(ctx, "api/v3/calendar", params)
	if err != nil {
		return nil, err
	}
	for _, raw := range asList(calendar) {
		data.Upcoming = append(data.Upcoming, arrItem(asMap(raw), data.App))
	}
	sort.Slice(data.Upcoming, func(i, j int) bool { return data.Upcoming[i].At.Before(data.Upcoming[j].At) })
	return data, nil
}

// arrItem names an episode "Show 2x05" or a movie by its title and first
// release date.
func arrItem(m map[string]any, app string) ArrItem {
	if app == sonarrApp {
		title := asStr(asMap(m["series"])["title"]) + " " + strconv.Itoa(int(asFloat(m["seasonNumber"]))) + "x" + pad2(int(asFloat(m["episodeNumber"])))
		return ArrItem{Title: title, At: parseTime(m["airDateUtc"])}
	}
	at := time.Time{}
	for _, key := range []string{"digitalRelease", "physicalRelease", "inCinemas"} {
		if t := parseTime(m[key]); !t.IsZero() && (at.IsZero() || t.Before(at)) {
			at = t
		}
	}
	return ArrItem{Title: asStr(m["title"]), At: at}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// ── Demo ──

func DemoMediaServer() *MediaServerDataset {
	return &MediaServerDataset{URL: "https://jellyfin.demo", Kind: mediaJellyfin, Version: "10.10.7", Movies: 1204, Series: 86, Episodes: 4310,
		Streams: []Stream{{User: "anna", Title: "Dark"}, {User: "ben", Title: "Arrival"}}}
}

func DemoArr(now time.Time) *ArrDataset {
	return &ArrDataset{URL: "https://sonarr.demo", App: sonarrApp, Version: "4.0.15", Queue: 3, Missing: 12,
		Stuck:    []string{"Andor.S02E04"},
		Health:   []ArrHealth{{Level: arrWarning, Message: "Indexer Nyaa is unavailable"}},
		Upcoming: []ArrItem{{Title: "Severance 2x09", At: now.Add(26 * time.Hour)}, {Title: "The Bear 4x01", At: now.Add(80 * time.Hour)}}}
}

func init() {
	Register(MediaServerData{})
	Register(testOf{MediaServerData{}, func(d any) map[string]any { return map[string]any{"version": d.(*MediaServerDataset).Version} }})
	Register(ArrData{})
	Register(testOf{ArrData{}, func(d any) map[string]any {
		a := d.(*ArrDataset)
		return map[string]any{"version": a.App + " " + a.Version}
	}})
}
