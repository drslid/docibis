package httpapi

import (
	"cmp"
	"context"
	"math"
	"slices"
	"sort"
	"time"

	"docibis/internal/store"
)

func renderEntry(r store.Render) RenderEntry {
	e := RenderEntry{
		Id: r.ID, CreatedAt: r.CreatedAt, Source: RenderEntrySource(r.Source), Actor: r.Actor, Template: r.Template,
		Format: r.Format, Status: RenderEntryStatus(r.Status), Size: r.Size, DurationMs: r.DurationMs, Engine: r.Engine,
	}
	if r.Error != "" {
		e.Error = &r.Error
	}
	return e
}

// top lists the most frequent names, most frequent first.
func top(counts map[string]int, n int) []NameCount {
	out := make([]NameCount, 0, len(counts))
	for name, count := range counts {
		out = append(out, NameCount{Name: name, Count: count})
	}
	slices.SortFunc(out, func(a, b NameCount) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Name, b.Name))
	})
	return out[:min(n, len(out))]
}

// dashboard counts the documents of the last 30 days (UTC), refusals and
// failures apart; with only (not nil), the documents of these templates alone.
func (s *Server) dashboard(ctx context.Context, only []int64) (Dashboard, error) {
	st := s.db.Load()
	now := time.Now().UTC()
	first := now.Truncate(24*time.Hour).AddDate(0, 0, -29)
	weekStart := now.Truncate(24*time.Hour).AddDate(0, 0, -6)
	rows, err := st.RenderTimes(ctx, first)
	if err != nil {
		return Dashboard{}, err
	}
	out := Dashboard{Days: make([]DayPoint, 30)}
	day := map[string]int{}
	for i := range out.Days {
		d := first.AddDate(0, 0, i).Format(time.DateOnly)
		out.Days[i], day[d] = DayPoint{Date: d}, i
	}
	today := now.Format(time.DateOnly)
	sources, templates, actors := map[string]int{}, map[string]int{}, map[string]int{}
	var durations []int
	for _, r := range rows {
		d := r.CreatedAt.UTC().Format(time.DateOnly)
		i, ok := day[d]
		if !ok || only != nil && !slices.Contains(only, r.TemplateID) {
			continue
		}
		p := &out.Days[i]
		switch r.Status {
		case "ok":
			p.Ok++
			out.Last30d++
			durations = append(durations, r.DurationMs)
			if d == today {
				out.Today++
			}
			if !r.CreatedAt.Before(weekStart) {
				out.Last7d++
			}
		case "refused":
			p.Refused++
			out.Refused30d++
		default:
			p.Errors++
			out.Errors30d++
		}
		sources[r.Source]++
		if r.Template != "" {
			templates[r.Template]++
		}
		if r.Actor != "" {
			actors[r.Actor]++
		}
	}
	if total := out.Last30d + out.Refused30d + out.Errors30d; total > 0 {
		out.SuccessRate = float32(math.Round(float64(out.Last30d)*1000/float64(total)) / 10)
	}
	sort.Ints(durations)
	out.P50Ms, out.P95Ms = percentile(durations, 0.5), percentile(durations, 0.95)
	out.Sources, out.TopTemplates, out.TopActors = top(sources, 5), top(templates, 5), top(actors, 5)

	keys, err := st.APIKeys(ctx)
	if err != nil {
		return Dashboard{}, err
	}
	for _, k := range keys {
		if apiKey(k).Status == ApiKeyStatusActive {
			out.ActiveKeys++
		}
	}
	active := true
	users, err := st.Users(ctx, "", "", &active)
	if err != nil {
		return Dashboard{}, err
	}
	out.Users = len(users)
	recent, _, err := st.Renders(ctx, store.RenderFilter{TemplateIDs: only}, store.Page{Limit: 8})
	if err != nil {
		return Dashboard{}, err
	}
	out.Recent = make([]RenderEntry, 0, len(recent))
	for _, r := range recent {
		out.Recent = append(out.Recent, renderEntry(r))
	}
	return out, nil
}

func (s *Server) GetDashboard(ctx context.Context, _ GetDashboardRequestObject) (GetDashboardResponseObject, error) {
	out, err := s.dashboard(ctx, nil)
	if err != nil {
		return nil, err
	}
	return GetDashboard200JSONResponse(out), nil
}
