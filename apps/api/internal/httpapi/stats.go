package httpapi

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"sort"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"
)

func statsDays(days *int) int {
	if days == nil {
		return 30
	}
	return min(max(*days, 1), 365)
}

// ListTemplateUses tells, for each template of the library, how many
// documents it produced: the library shows the unused ones.
func (s *Server) ListTemplateUses(ctx context.Context, req ListTemplateUsesRequestObject) (ListTemplateUsesResponseObject, error) {
	days := statsDays(req.Params.Days)
	uses, err := s.db.Load().TemplateUses(ctx, time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1-days))
	if err != nil {
		return nil, err
	}
	out := make(ListTemplateUses200JSONResponse, 0, len(uses))
	for id, u := range uses {
		out = append(out, TemplateUse{Id: id, Count: u.Count, Errors: u.Errors, Refused: u.Refused, LastUsedAt: u.LastUsedAt})
	}
	slices.SortFunc(out, func(a, b TemplateUse) int { return cmp.Compare(a.Id, b.Id) })
	return out, nil
}

// GetTemplateStats sums up the documents of a template: per day, who
// produces it, with which version, how fast.
func (s *Server) GetTemplateStats(ctx context.Context, req GetTemplateStatsRequestObject) (GetTemplateStatsResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetTemplateStats404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	days := statsDays(req.Params.Days)
	first := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1-days)
	renders, err := st.TemplateRenders(ctx, t.ID, first)
	if err != nil {
		return nil, err
	}
	out := TemplateStats{Days: days, Series: make([]TemplateDay, days), Callers: []TemplateCaller{}, Versions: []VersionUse{}}
	index := map[string]int{}
	for i := range out.Series {
		d := first.AddDate(0, 0, i)
		out.Series[i].Day = openapi_types.Date{Time: d}
		index[d.Format(time.DateOnly)] = i
	}
	type caller struct{ source, actor string }
	callers, versions := map[caller]int{}, map[int]int{}
	var durations []int
	for _, r := range renders {
		out.Count++
		i, inRange := index[r.CreatedAt.UTC().Format(time.DateOnly)]
		if inRange {
			out.Series[i].Count++
		}
		switch r.Status {
		case "ok":
			durations = append(durations, r.DurationMs)
		case "refused":
			out.Refused++
		default:
			out.Errors++
			if inRange {
				out.Series[i].Errors++
			}
		}
		if r.Status != "ok" && r.Error != "" {
			out.LastError = ptr(r.Error)
		}
		callers[caller{r.Source, r.Actor}]++
		if r.TemplateVersion > 0 {
			versions[r.TemplateVersion]++
		}
		at := r.CreatedAt
		out.LastUsedAt = &at
	}
	sort.Ints(durations)
	out.P50Ms, out.P95Ms = percentile(durations, 0.5), percentile(durations, 0.95)
	for c, n := range callers {
		out.Callers = append(out.Callers, TemplateCaller{Source: c.source, Actor: c.actor, Count: n})
	}
	slices.SortFunc(out.Callers, func(a, b TemplateCaller) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Actor, b.Actor))
	})
	if len(out.Callers) > 20 {
		out.Callers = out.Callers[:20]
	}
	for v, n := range versions {
		out.Versions = append(out.Versions, VersionUse{Version: v, Count: n})
	}
	slices.SortFunc(out.Versions, func(a, b VersionUse) int { return cmp.Compare(b.Version, a.Version) })
	return GetTemplateStats200JSONResponse(out), nil
}
