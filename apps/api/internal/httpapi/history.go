package httpapi

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"math"
	"mime"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"docibis/internal/render"
	"docibis/internal/store"
	"docibis/internal/version"
)

func (s *Server) ListRenders(ctx context.Context, req ListRendersRequestObject) (ListRendersResponseObject, error) {
	p := req.Params
	f := store.RenderFilter{Text: trimmed(p.Q)}
	if p.Status != nil {
		f.Status = string(*p.Status)
	}
	if p.Source != nil {
		f.Source = string(*p.Source)
	}
	if p.Format != nil {
		f.Format = string(*p.Format)
	}
	rows, total, err := s.db.Load().Renders(ctx, f, page(p.Limit, p.Offset))
	if err != nil {
		return nil, err
	}
	out := RenderPage{Items: make([]RenderEntry, 0, len(rows)), Total: total}
	for _, r := range rows {
		e := RenderEntry{
			Id: r.ID, CreatedAt: r.CreatedAt, Source: RenderEntrySource(r.Source), Actor: r.Actor, Template: r.Template,
			Format: r.Format, Status: RenderEntryStatus(r.Status), Size: r.Size, DurationMs: r.DurationMs, Engine: r.Engine,
		}
		if r.Error != "" {
			e.Error = &r.Error
		}
		e.HasData = &r.HasData
		if r.RequestID != "" {
			e.RequestId = &r.RequestID
		}
		if r.TemplateVersion != 0 {
			e.TemplateVersion = &r.TemplateVersion
		}
		if r.Options != "" {
			e.Options = &r.Options
		}
		out.Items = append(out.Items, e)
	}
	return ListRenders200JSONResponse(out), nil
}

// RegenerateRender produces again a document of the history with the data
// and the template kept with it. The new document is not added to the
// history; the audit journal notes who saw it.
func (s *Server) RegenerateRender(ctx context.Context, req RegenerateRenderRequestObject) (RegenerateRenderResponseObject, error) {
	st := s.db.Load()
	r, ok, err := st.RenderByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RegenerateRender404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Document inconnu."))}, nil
	}
	notKept := RegenerateRender400ApplicationProblemPlusJSONResponse{badRequest("Les données de ce document n'ont pas été gardées : il ne peut pas être produit à nouveau.")}
	if len(r.Data) == 0 {
		return notKept, nil
	}
	data, err := gunzipped(r.Data, 200<<20)
	if err != nil {
		return notKept, nil
	}
	f := tryForm{name: r.Template, data: data, hasData: true, opts: renderOptions{pdfa: "none"}}
	if r.Options != "" {
		var fields map[string]string
		if json.Unmarshal([]byte(r.Options), &fields) == nil {
			for name, value := range fields {
				_, _ = f.opts.set(name, value)
			}
		}
	}
	switch {
	case r.TemplateID != 0:
		t, found, err := st.TemplateByID(ctx, r.TemplateID)
		if err != nil {
			return nil, err
		}
		v, vFound, err := st.ServedVersion(ctx, r.TemplateID, r.TemplateVersion)
		if err != nil {
			return nil, err
		}
		if !found || !vFound {
			return RegenerateRender404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "La version du modèle de ce document n'existe plus dans la bibliothèque."))}, nil
		}
		f.template, f.prepared, f.check, f.libID, f.libVersion = v.Content, v.Prepared, t.DataCheck, t.ID, v.Number
		if v.Fields != "" {
			_ = json.Unmarshal([]byte(v.Fields), &f.fields)
		}
	case r.TemplateSHA != "":
		content, found, err := st.TemplateBlob(ctx, r.TemplateSHA)
		if err != nil {
			return nil, err
		}
		if !found {
			return RegenerateRender404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Le modèle de ce document n'est plus gardé."))}, nil
		}
		f.template = content
	default:
		return notKept, nil
	}
	me, _ := userFrom(ctx)
	start := time.Now()
	var doc document
	if s.renderer.Load() == nil {
		err = notReady
	} else {
		doc, err = s.produceForm(ctx, f, r.Format)
	}
	s.logDocument(ctx, "regenerate", me.Username, doc, err, time.Since(start))
	if err != nil {
		switch status, p := s.failure(err); status {
		case http.StatusServiceUnavailable:
			return RegenerateRender503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		case http.StatusNotFound:
			return RegenerateRender404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		default:
			return RegenerateRender400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	s.audit(ctx, me.Username, "render.regenerate", fmt.Sprintf("render:%d", r.ID), r.Template)
	disposition := mime.FormatMediaType("inline", map[string]string{"filename": doc.fileName()})
	headers := RegenerateRender200ResponseHeaders{ContentDisposition: &disposition}
	body, size := bytes.NewReader(doc.content), int64(len(doc.content))
	if doc.format == "docx" {
		return RegenerateRender200ApplicationvndOpenxmlformatsOfficedocumentWordprocessingmlDocumentResponse{Body: body, ContentLength: size, Headers: headers}, nil
	}
	return RegenerateRender200ApplicationpdfResponse{Body: body, ContentLength: size, Headers: headers}, nil
}

func (s *Server) ListAudit(ctx context.Context, req ListAuditRequestObject) (ListAuditResponseObject, error) {
	p := req.Params
	rows, total, actions, err := s.db.Load().AuditEntries(ctx, store.AuditFilter{Text: trimmed(p.Q), Action: trimmed(p.Action), Actor: trimmed(p.Actor)}, page(p.Limit, p.Offset))
	if err != nil {
		return nil, err
	}
	out := AuditPage{Items: make([]AuditEntry, 0, len(rows)), Total: total, Actions: actions}
	if out.Actions == nil {
		out.Actions = []string{}
	}
	for _, e := range rows {
		out.Items = append(out.Items, AuditEntry{Id: e.ID, CreatedAt: e.CreatedAt, Actor: e.Actor, Action: e.Action, Target: e.Target, Details: e.Details, Ip: e.IP})
	}
	return ListAudit200JSONResponse(out), nil
}

const maxAuditExport = 50000

// ExportAudit writes the journal as CSV for Excel: semicolons, UTF-8 with
// its mark, the newest first.
func (s *Server) ExportAudit(ctx context.Context, req ExportAuditRequestObject) (ExportAuditResponseObject, error) {
	p := req.Params
	rows, err := s.db.Load().AuditExport(ctx, store.AuditFilter{Text: trimmed(p.Q), Action: trimmed(p.Action), Actor: trimmed(p.Actor)}, maxAuditExport)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	w.Comma = ';'
	_ = w.Write([]string{"date", "auteur", "action", "cible", "détails", "adresse IP"})
	for _, e := range rows {
		_ = w.Write([]string{e.CreatedAt.UTC().Format(time.RFC3339), spreadsheetSafe(e.Actor), e.Action, spreadsheetSafe(e.Target), spreadsheetSafe(e.Details), e.IP})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "audit.export", "", fmt.Sprintf("%d lignes", len(rows)))
	name := fmt.Sprintf("docibis-journal-%s.csv", time.Now().Format("2006-01-02"))
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	return ExportAudit200TextcsvResponse{Body: bytes.NewReader(buf.Bytes()), ContentLength: int64(buf.Len()),
		Headers: ExportAudit200ResponseHeaders{ContentDisposition: &disposition}}, nil
}

// spreadsheetSafe keeps a cell from being read as a formula by Excel.
func spreadsheetSafe(v string) string {
	if v != "" && strings.ContainsRune("=+-@\t\r", rune(v[0])) {
		return "'" + v
	}
	return v
}

func page(limit, offset *int) store.Page {
	p := store.Page{Limit: 50}
	if limit != nil {
		p.Limit = *limit
	}
	if offset != nil {
		p.Offset = *offset
	}
	return p
}

// traffic counts the requests and the server errors of the last hour, minute
// by minute. Health checks are left out: the load balancer calls them all the
// time.
type traffic struct {
	mu      sync.Mutex
	minutes [60]minute
}

type minute struct {
	start            time.Time
	requests, errors int
}

func (t *traffic) observe(status int, at time.Time) {
	m := at.Truncate(time.Minute)
	t.mu.Lock()
	defer t.mu.Unlock()
	slot := &t.minutes[m.Unix()/60%60]
	if !slot.start.Equal(m) {
		*slot = minute{start: m}
	}
	slot.requests++
	if status >= 500 {
		slot.errors++
	}
}

// series returns the last 60 minutes, the oldest first.
func (t *traffic) series(now time.Time) []TrafficPoint {
	t.mu.Lock()
	defer t.mu.Unlock()
	end := now.Truncate(time.Minute)
	out := make([]TrafficPoint, 0, 60)
	for i := 59; i >= 0; i-- {
		m := end.Add(-time.Duration(i) * time.Minute)
		p := TrafficPoint{T: m}
		if slot := t.minutes[m.Unix()/60%60]; slot.start.Equal(m) {
			p.Requests, p.Errors = slot.requests, slot.errors
		}
		out = append(out, p)
	}
	return out
}

func percentile(sorted []int, p float64) int {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[int(math.Ceil(p*float64(len(sorted))))-1]
}

func (s *Server) GetMonitoring(ctx context.Context, _ GetMonitoringRequestObject) (GetMonitoringResponseObject, error) {
	st := s.db.Load()
	now := time.Now().UTC()
	out := Monitoring{At: now, Version: version.Version, Instance: s.cfg.Load().String("instance_name"), UptimeSeconds: int64(time.Since(s.started).Seconds())}
	var problems []string

	ping, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out.Database.Engine = string(st.Engine())
	out.Database.Ok = st.Ping(ping) == nil
	r := st.Replication()
	out.Database.Replication = Replication{Enabled: r.Enabled, Locked: r.Locked}
	if r.Target != "" {
		out.Database.Replication.Target = &r.Target
	}
	if !out.Database.Ok {
		problems = append(problems, "La base ne répond pas.")
	}
	if r.LeaseLost {
		problems = append(problems, "Une autre tâche a pris la base.")
	}

	if box := s.renderer.Load(); box != nil {
		out.Workers.Ready = true
		if p, ok := box.Renderer.(interface{ Stats() render.Stats }); ok {
			stats := p.Stats()
			out.Workers.Total, out.Workers.Busy, out.Workers.Queued = stats.Workers, stats.Busy, stats.Queued
		}
	} else {
		problems = append(problems, "Les workers LibreOffice ne sont pas prêts.")
	}

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	out.Memory.HeapMb = float32(math.Round(float64(mem.HeapAlloc)/(1<<20)*10) / 10)
	out.Memory.SystemMb = float32(math.Round(float64(mem.Sys)/(1<<20)*10) / 10)
	out.Memory.Goroutines = runtime.NumGoroutine()
	out.System = s.system.usage()

	out.Traffic.Series = s.traffic.series(now)
	last := out.Traffic.Series[len(out.Traffic.Series)-2]
	out.Traffic.RequestsPerMinute, out.Traffic.ErrorsPerMinute = last.Requests, last.Errors

	times, err := st.RenderTimes(ctx, now.Add(-time.Hour))
	if err != nil {
		return nil, err
	}
	var durations []int
	perMinute := map[int64][]store.Render{}
	for _, t := range times {
		switch t.Status {
		case "refused":
			out.Renders.RefusedLastHour++
			continue
		case "error":
			out.Renders.ErrorsLastHour++
		default:
			out.Renders.LastHour++
			durations = append(durations, t.DurationMs)
		}
		m := t.CreatedAt.Truncate(time.Minute).Unix()
		perMinute[m] = append(perMinute[m], t)
	}
	sort.Ints(durations)
	out.Renders.P50Ms, out.Renders.P95Ms = percentile(durations, 0.5), percentile(durations, 0.95)
	for i := 59; i >= 0; i-- {
		m := now.Truncate(time.Minute).Add(-time.Duration(i) * time.Minute)
		point := RenderPoint{T: m}
		var ds []int
		for _, t := range perMinute[m.Unix()] {
			point.Count++
			if t.Status == "error" {
				point.Errors++
			} else {
				ds = append(ds, t.DurationMs)
			}
		}
		sort.Ints(ds)
		point.P95Ms = percentile(ds, 0.95)
		out.Renders.Series = append(out.Renders.Series, point)
	}
	since := now.Add(-24 * time.Hour)
	if _, out.Renders.Last24h, err = st.Renders(ctx, store.RenderFilter{Status: "ok", Since: &since}, store.Page{Limit: 1}); err != nil {
		return nil, err
	}
	if out.Renders.ErrorsLastHour > 0 {
		problems = append(problems, "Le service a échoué sur des documents dans l'heure : voir Documents produits.")
	}

	out.Health = MonitoringHealthOk
	switch {
	case !out.Database.Ok:
		out.Health = MonitoringHealthDown
	case len(problems) > 0:
		out.Health = MonitoringHealthDegraded
	}
	if len(problems) > 0 {
		out.Problems = &problems
	}
	return GetMonitoring200JSONResponse(out), nil
}
