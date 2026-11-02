package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"docibis/internal/render"
	"docibis/internal/version"
)

// Prometheus: /metrics answers with a bearer token created in
// the Monitoring page, or set by METRICS_TOKEN; without a token it does not exist.
const metricsTokenKey = "metrics.token"

type metricsTokenState struct {
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"createdAt"`
	CreatedBy string    `json:"createdBy"`
}

type metrics struct {
	requests  *prometheus.CounterVec
	docs      *prometheus.CounterVec
	duration  *prometheus.HistogramVec
	throttles prometheus.Counter
	handler   http.Handler
}

var (
	descBuild   = prometheus.NewDesc("docibis_build_info", "Version and instance.", []string{"version", "instance"}, nil)
	descStart   = prometheus.NewDesc("docibis_start_time_seconds", "Start of the process, Unix time.", nil, nil)
	descReady   = prometheus.NewDesc("docibis_engine_ready", "1 when the LibreOffice workers are ready.", nil, nil)
	descWorkers = prometheus.NewDesc("docibis_workers", "LibreOffice workers by state: busy, idle.", []string{"state"}, nil)
	descQueue   = prometheus.NewDesc("docibis_queue_requests", "Documents waiting for a worker.", nil, nil)
	descDB      = prometheus.NewDesc("docibis_database_up", "1 when the database answers.", []string{"engine"}, nil)
)

func newMetrics(s *Server) *metrics {
	m := &metrics{
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "docibis_http_requests_total",
			Help: "HTTP requests by class of status (health checks apart)."}, []string{"code"}),
		docs: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "docibis_documents_total",
			Help: "Documents asked, by origin (console, api, mcp) and result (ok, refused, error)."}, []string{"source", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "docibis_document_duration_seconds",
			Help: "Time to produce a document, by origin.", Buckets: []float64{.05, .1, .25, .5, 1, 2, 5, 10, 30}}, []string{"source"}),
		throttles: prometheus.NewCounter(prometheus.CounterOpts{Name: "docibis_api_throttled_total",
			Help: "API calls refused because a key went over its calls per minute."}),
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(m.requests, m.docs, m.duration, m.throttles, stateCollector{s})
	m.handler = promhttp.HandlerFor(reg, promhttp.HandlerOpts{})
	return m
}

func (m *metrics) request(status int) {
	m.requests.WithLabelValues(strconv.Itoa(status/100) + "xx").Inc()
}

func (m *metrics) document(source, status string, took time.Duration) {
	m.docs.WithLabelValues(source, status).Inc()
	if status == "ok" {
		m.duration.WithLabelValues(source).Observe(took.Seconds())
	}
}

func (m *metrics) throttled() { m.throttles.Inc() }

// stateCollector reads the state of the service at each scrape.
type stateCollector struct{ s *Server }

func (c stateCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{descBuild, descStart, descReady, descWorkers, descQueue, descDB} {
		ch <- d
	}
}

func (c stateCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.s
	instance := ""
	if cfg := s.cfg.Load(); cfg != nil {
		instance = cfg.String("instance_name")
	}
	ch <- prometheus.MustNewConstMetric(descBuild, prometheus.GaugeValue, 1, version.Version, instance)
	ch <- prometheus.MustNewConstMetric(descStart, prometheus.GaugeValue, float64(s.started.Unix()))
	ready := 0.0
	if box := s.renderer.Load(); box != nil {
		ready = 1
		if p, ok := box.Renderer.(interface{ Stats() render.Stats }); ok {
			st := p.Stats()
			ch <- prometheus.MustNewConstMetric(descWorkers, prometheus.GaugeValue, float64(st.Busy), "busy")
			ch <- prometheus.MustNewConstMetric(descWorkers, prometheus.GaugeValue, float64(st.Workers-st.Busy), "idle")
			ch <- prometheus.MustNewConstMetric(descQueue, prometheus.GaugeValue, float64(st.Queued))
		}
	}
	ch <- prometheus.MustNewConstMetric(descReady, prometheus.GaugeValue, ready)
	if st := s.db.Load(); st != nil {
		up := 0.0
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if st.Ping(ctx) == nil {
			up = 1
		}
		ch <- prometheus.MustNewConstMetric(descDB, prometheus.GaugeValue, up, string(st.Engine()))
	}
}

// envMetricsToken is METRICS_TOKEN when long enough to be a secret.
func envMetricsToken() string {
	if t := strings.TrimSpace(os.Getenv("METRICS_TOKEN")); len(t) >= 32 {
		return t
	}
	return ""
}

func (s *Server) metricsToken(ctx context.Context) (metricsTokenState, bool, error) {
	var t metricsTokenState
	raw, ok, err := s.db.Load().Setting(ctx, metricsTokenKey)
	if err != nil || !ok || raw == "" {
		return t, false, err
	}
	return t, json.Unmarshal([]byte(raw), &t) == nil && t.Hash != "", nil
}

func (s *Server) metricsTokenOut(ctx context.Context) (MetricsToken, error) {
	if envMetricsToken() != "" {
		return MetricsToken{Configured: true, FromEnv: true}, nil
	}
	t, ok, err := s.metricsToken(ctx)
	if err != nil || !ok {
		return MetricsToken{}, err
	}
	return MetricsToken{Configured: true, CreatedAt: &t.CreatedAt, CreatedBy: &t.CreatedBy}, nil
}

func (s *Server) GetMetricsToken(ctx context.Context, _ GetMetricsTokenRequestObject) (GetMetricsTokenResponseObject, error) {
	out, err := s.metricsTokenOut(ctx)
	if err != nil {
		return nil, err
	}
	return GetMetricsToken200JSONResponse(out), nil
}

var metricsLocked = problem(http.StatusConflict, "Le jeton est fixé par la variable METRICS_TOKEN.")

func (s *Server) CreateMetricsToken(ctx context.Context, _ CreateMetricsTokenRequestObject) (CreateMetricsTokenResponseObject, error) {
	if envMetricsToken() != "" {
		return CreateMetricsToken409ApplicationProblemPlusJSONResponse(metricsLocked), nil
	}
	me, _ := userFrom(ctx)
	token := "dim_" + randomText(32)
	raw, err := json.Marshal(metricsTokenState{Hash: hashToken(token), CreatedAt: time.Now().UTC(), CreatedBy: me.Username})
	if err != nil {
		return nil, err
	}
	if err := s.db.Load().SetSetting(ctx, metricsTokenKey, string(raw)); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "metrics.token.create", "", "")
	out, err := s.metricsTokenOut(ctx)
	if err != nil {
		return nil, err
	}
	out.Token = &token
	return CreateMetricsToken200JSONResponse(out), nil
}

func (s *Server) RevokeMetricsToken(ctx context.Context, _ RevokeMetricsTokenRequestObject) (RevokeMetricsTokenResponseObject, error) {
	if envMetricsToken() != "" {
		return RevokeMetricsToken409ApplicationProblemPlusJSONResponse(metricsLocked), nil
	}
	if err := s.db.Load().SetSetting(ctx, metricsTokenKey, ""); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "metrics.token.revoke", "", "")
	return RevokeMetricsToken200JSONResponse(MetricsToken{}), nil
}

// serveMetrics answers Prometheus when its token is right.
func (s *Server) serveMetrics(c *echo.Context) error {
	r := c.Request()
	if s.db.Load() == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, starting)
	}
	want := ""
	if env := envMetricsToken(); env != "" {
		want = hashToken(env)
	} else if t, ok, err := s.metricsToken(r.Context()); err != nil {
		return err
	} else if ok {
		want = t.Hash
	}
	if want == "" {
		return echo.NewHTTPError(http.StatusNotFound, "Prometheus n'est pas activé : créez un jeton dans la page Monitoring.")
	}
	given, found := strings.CutPrefix(r.Header.Get(echo.HeaderAuthorization), "Bearer ")
	if !found || subtle.ConstantTimeCompare([]byte(hashToken(strings.TrimSpace(given))), []byte(want)) != 1 {
		c.Response().Header().Set("WWW-Authenticate", `Bearer realm="DocIbis metrics"`)
		return echo.NewHTTPError(http.StatusUnauthorized, "Jeton Prometheus absent ou faux.")
	}
	s.metrics.handler.ServeHTTP(c.Response(), r)
	return nil
}
