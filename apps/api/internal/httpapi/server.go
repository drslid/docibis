package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"

	"docibis/internal/settings"
	"docibis/internal/store"
	"docibis/internal/version"
)

// Server answers the requests. It starts before the database is open:
// /healthz answers at once, /readyz and the API wait for SetStore.
type Server struct {
	logger   *slog.Logger
	startup  []StartupSetting
	db       atomic.Pointer[store.Store]
	cfg      atomic.Pointer[settings.Service]
	setup    atomic.Pointer[SetupCode]
	logins   limiter
	renderer atomic.Pointer[rendererBox]
	prepared preparedCache
	started  time.Time
	traffic  traffic
	metrics  *metrics
	rates    rateLimits
	mfa      mfaState
	touches  sessionTouches
	alerts   alertEngine
	images   imageCache
	system   systemSampler
	overlays overlayCache
	// Background work of the library: reading imported templates, comparing
	// with the references.
	describing, describeQueued, checking atomic.Bool
	// releaseNoted is set once the note of this version is written.
	releaseNoted atomic.Bool
	// key seals the second-factor secrets (APP_SECRET_KEY), nil without it.
	key []byte
}

var _ StrictServerInterface = (*Server)(nil)

// NewServer prepares the handlers; startup lists the read-only settings
// shown by the configuration page.
func NewServer(startup []StartupSetting, logger *slog.Logger) *Server {
	s := &Server{startup: startup, logger: logger, started: time.Now(), key: appKey()}
	s.metrics = newMetrics(s)
	if t := os.Getenv("METRICS_TOKEN"); t != "" && envMetricsToken() == "" {
		logger.Warn("METRICS_TOKEN ignored: it must have at least 32 characters")
	}
	if k := os.Getenv("APP_SECRET_KEY"); k != "" && s.key == nil {
		logger.Warn("APP_SECRET_KEY ignored: it must have at least 32 characters")
	}
	return s
}

// SetStore makes the service ready once the database is open. setup is the
// code that creates the first administrator, nil when one exists.
func (s *Server) SetStore(st *store.Store, cfg *settings.Service, setup *SetupCode) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s.sealSecrets(ctx, st)
	s.cfg.Store(cfg)
	s.setup.Store(setup)
	s.db.Store(st)
}

// Handler builds the HTTP router.
func (s *Server) Handler() http.Handler {
	e := echo.NewWithConfig(echo.Config{Logger: s.logger, HTTPErrorHandler: writeError})
	observe := func(status int, at time.Time) {
		s.traffic.observe(status, at)
		s.metrics.request(status)
	}
	e.Use(middleware.RequestID(), withRequestID, securityHeaders, logRequests(s.logger, observe), middleware.Recover(), s.guard, s.keyGuard, s.limitBody)
	RegisterHandlers(e, NewStrictHandler(s, nil))
	mcp := echo.WrapHandler(s.mcpHandler())
	e.POST(mcpPath, mcp)
	e.GET(mcpPath, mcp)
	e.DELETE(mcpPath, mcp)
	e.GET("/metrics", s.serveMetrics)
	return e
}

const starting = "Le service démarre : la base n'est pas encore ouverte."

func (s *Server) GetHealthz(context.Context, GetHealthzRequestObject) (GetHealthzResponseObject, error) {
	return GetHealthz200JSONResponse{Status: HealthStatusOk}, nil
}

func (s *Server) GetReadyz(ctx context.Context, _ GetReadyzRequestObject) (GetReadyzResponseObject, error) {
	st := s.db.Load()
	switch {
	case st == nil:
		return GetReadyz503ApplicationProblemPlusJSONResponse(unavailable(starting)), nil
	case st.Replication().LeaseLost:
		return GetReadyz503ApplicationProblemPlusJSONResponse(unavailable("Une autre tâche a pris la base : celle-ci doit s'arrêter.")), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := st.Ping(ctx); err != nil {
		s.logger.Warn("database does not answer", "error", err)
		return GetReadyz503ApplicationProblemPlusJSONResponse(unavailable("La base ne répond pas.")), nil
	}
	return GetReadyz200JSONResponse{Status: HealthStatusReady}, nil
}

func (s *Server) GetInfo(context.Context, GetInfoRequestObject) (GetInfoResponseObject, error) {
	st := s.db.Load()
	if st == nil {
		return GetInfo503ApplicationProblemPlusJSONResponse(unavailable(starting)), nil
	}
	r := st.Replication()
	rep := Replication{Enabled: r.Enabled, Locked: r.Locked}
	if r.Target != "" {
		rep.Target = &r.Target
	}
	if r.Locked {
		rep.LeaseLost = &r.LeaseLost
	}
	return GetInfo200JSONResponse{
		Name:     "DocIbis",
		Version:  version.Version,
		Instance: s.cfg.Load().String("instance_name"),
		Database: DatabaseInfo{Engine: DatabaseInfoEngine(st.Engine()), Replication: rep},
	}, nil
}

func unavailable(detail string) Problem {
	return Problem{Status: http.StatusServiceUnavailable, Title: http.StatusText(http.StatusServiceUnavailable), Detail: &detail}
}

// writeError answers every error in the RFC 9457 format. The cause of a 5xx
// stays in the logs: the client only sees the status.
func writeError(c *echo.Context, err error) {
	if r, _ := echo.UnwrapResponse(c.Response()); r != nil && r.Committed {
		return
	}
	code := http.StatusInternalServerError
	var sc echo.HTTPStatusCoder
	if errors.As(err, &sc) && sc.StatusCode() != 0 {
		code = sc.StatusCode()
	}
	p := Problem{Status: code, Title: http.StatusText(code)}
	var he *echo.HTTPError
	if code < 500 && errors.As(err, &he) && he.Message != "" && he.Message != p.Title {
		p.Detail = &he.Message
	}
	w := c.Response()
	w.Header().Set(echo.HeaderContentType, "application/problem+json")
	w.WriteHeader(code)
	if c.Request().Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(p)
	}
}

// logRequests writes one line per request: method, route, status, duration
// and request ID. Never bodies, query strings or headers, which may carry
// personal data or keys. Successful health checks and Prometheus scrapes are
// not logged nor counted.
func logRequests(logger *slog.Logger, observe func(status int, at time.Time)) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		HandleError:  true,
		LogLatency:   true,
		LogMethod:    true,
		LogRoutePath: true,
		LogURIPath:   true,
		LogRequestID: true,
		LogStatus:    true,
		LogValuesFunc: func(c *echo.Context, v middleware.RequestLoggerValues) error {
			if v.Status < 400 && (v.RoutePath == "/healthz" || v.RoutePath == "/readyz" || v.RoutePath == "/metrics") {
				return nil
			}
			observe(v.Status, time.Now())
			route := v.RoutePath
			if route == "" || route == "/*" {
				route = v.URIPath // no route matched: keep the path to understand the 404
			}
			level := slog.LevelInfo
			switch {
			case v.Status >= 500:
				level = slog.LevelError
			case v.Status >= 400:
				level = slog.LevelWarn
			}
			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("route", route),
				slog.Int("status", v.Status),
				slog.Int64("duration_ms", v.Latency.Milliseconds()),
				slog.String("request_id", v.RequestID),
			}
			if v.Error != nil && v.Status >= 500 {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
			}
			logger.LogAttrs(c.Request().Context(), level, "request", attrs...)
			return nil
		},
	})
}
