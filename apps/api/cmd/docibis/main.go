// Command docibis is the DocIbis server.
//
//	docibis serve              start the service (default)
//	docibis healthcheck [ready] check a running service (for Docker)
//	docibis backup             back up the database now
//	docibis backups            list the backups
//	docibis restore NAME       replace the database with a backup (service stopped)
//	docibis bench files...     measure conversions with warm workers
//	docibis sample template.odt  example data for a template
//	docibis merge …            fill a template with JSON data
//	docibis analyze files...   engine each template can use, and why
//	docibis version            print the version
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"docibis/internal/backup"
	"docibis/internal/config"
	"docibis/internal/httpapi"
	"docibis/internal/render"
	"docibis/internal/settings"
	"docibis/internal/store"
	"docibis/internal/version"
)

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		os.Exit(serve())
	case "healthcheck":
		os.Exit(healthcheck(len(os.Args) > 2 && os.Args[2] == "ready"))
	case "backup":
		os.Exit(backupCmd(os.Args[2:]))
	case "backups":
		os.Exit(backupsCmd(os.Args[2:]))
	case "restore":
		os.Exit(restoreCmd(os.Args[2:]))
	case "bench":
		os.Exit(bench(os.Args[2:]))
	case "sample":
		os.Exit(sample(os.Args[2:]))
	case "merge":
		os.Exit(mergeCmd(os.Args[2:]))
	case "analyze":
		os.Exit(analyze(os.Args[2:]))
	case "version":
		fmt.Println(version.Version)
	default:
		fmt.Fprintln(os.Stderr, "usage: docibis [serve | healthcheck [ready] | backup | backups | restore NAME | bench | sample | merge | analyze | version]")
		os.Exit(2)
	}
}

func serve() int {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		logger.Error("invalid configuration", "error", err)
		return 1
	}
	level := new(slog.LevelVar)
	if l, err := cfg.SlogLevel(); err == nil {
		level.Set(l)
	}
	logger = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	// Two services on one SQLite file would damage it; a restore holds it too.
	release, err := lockData(cfg.DataDir)
	switch {
	case errors.Is(err, errDataLocked):
		logger.Error("another DocIbis process uses the data directory (a restore?): not starting", "data_dir", cfg.DataDir)
		return 1
	case err != nil:
		logger.Warn("data directory not locked", "error", err)
	default:
		defer release()
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	// HTTP answers first: /healthz says "alive" while the database is restored.
	api := httpapi.NewServer(startupSettings(cfg), logger)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// Long enough for a 300 MB import on a slow link and a batch of
		// documents; a client that trickles is cut.
		ReadTimeout:    10 * time.Minute,
		WriteTimeout:   10 * time.Minute,
		IdleTimeout:    2 * time.Minute,
		MaxHeaderBytes: 64 << 10,
	}
	served := make(chan error, 1)
	go func() { served <- srv.ListenAndServe() }()
	logger.Info("DocIbis starting", "version", version.Version, "listen", cfg.Listen,
		"instance", cfg.InstanceName, "locked", cfg.Locked)

	type opening struct {
		st  *store.Store
		err error
	}
	opened := make(chan opening, 1)
	go func() {
		replicaURL, _ := cfg.ReplicaURL()
		st, err := store.Open(ctx, store.Options{Path: cfg.SQLitePath(), URL: cfg.DatabaseURL, ReplicaURL: replicaURL}, logger)
		opened <- opening{st, err}
	}()

	var st *store.Store
	select {
	case err := <-served:
		logger.Error("HTTP server stopped", "error", err)
		stop()
		if o := <-opened; o.st != nil {
			_ = o.st.Close(context.Background())
		}
		return 1
	case o := <-opened:
		if o.err != nil {
			if ctx.Err() != nil {
				logger.Info("stopped while starting")
				return shutdown(srv, nil, nil, logger, 0)
			}
			logger.Error("cannot open the database", "error", o.err)
			return shutdown(srv, nil, nil, logger, 1)
		}
		st = o.st
	}
	saved, err := st.Settings(ctx)
	if err != nil {
		logger.Error("cannot read the settings", "error", err)
		return shutdown(srv, st, nil, logger, 1)
	}
	cfgs := settings.New(saved, os.Getenv)
	applyLevel := func() {
		var l slog.Level
		if l.UnmarshalText([]byte(cfgs.String("log_level"))) == nil {
			level.Set(l)
		}
	}
	applyLevel()
	cfgs.OnChange = func(keys []string) {
		if slices.Contains(keys, "log_level") {
			applyLevel()
		}
	}
	var setup *httpapi.SetupCode
	if n, err := st.CountUsers(ctx); err != nil {
		logger.Error("cannot read the accounts", "error", err)
		return shutdown(srv, st, nil, logger, 1)
	} else if n == 0 {
		setup = httpapi.NewSetupCode()
		logger.Warn("no administrator yet: open the console and enter this setup code", "setup_code", setup.String())
	}
	api.SetStore(st, cfgs, setup)
	logger.Info("DocIbis ready", "database", st.Engine(), "replication", st.Replication())
	if loc, err := backup.LocationFor(ctx, cfg.StateURL, cfg.DataDir); err != nil {
		logger.Error("no backups: their location cannot be used", "error", err)
	} else {
		api.SetBackups(loc, cfg.DataDir)
		if backup.Secret() == nil {
			logger.Warn("no backups: APP_SECRET_KEY (32 characters at least) encrypts them")
		}
		go api.WatchBackups(ctx)
	}
	// The fonts added are installed before the workers start.
	if _, err := api.SyncFonts(ctx); err != nil {
		logger.Error("fonts added not installed: documents use other fonts", "error", err)
	}
	stopWorkers := startWorkers(ctx, cfgs, api, logger)
	go api.WatchFonts(ctx)
	go api.WatchAlerts(ctx)
	go api.WatchSchedules(ctx)
	go api.WatchSystem(ctx)

	// The history of the documents produced is kept history_retention_days,
	// with the journal of the MCP calls; their data render_data_days.
	purge := func() {
		before := time.Now().AddDate(0, 0, -cfgs.Int("history_retention_days"))
		if n, err := st.PurgeRenders(ctx, before); err != nil {
			logger.Warn("history purge failed", "error", err)
		} else if n > 0 {
			logger.Info("history purged", "renders", n)
		}
		if n, err := st.PurgeRenderData(ctx, time.Now().AddDate(0, 0, -cfgs.Int("render_data_days"))); err != nil {
			logger.Warn("document data purge failed", "error", err)
		} else if n > 0 {
			logger.Info("document data purged", "renders", n)
		}
		if err := st.PurgeMCPCalls(ctx, before); err != nil {
			logger.Warn("MCP journal purge failed", "error", err)
		}
		if err := st.PurgeUsage(ctx, time.Now().AddDate(-1, -1, 0)); err != nil {
			logger.Warn("quota counts purge failed", "error", err)
		}
	}
	purge()
	hourly := time.NewTicker(time.Hour)
	defer hourly.Stop()

	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-hourly.C:
			purge()
		case <-ctx.Done():
			logger.Info("stopping")
			return shutdown(srv, st, stopWorkers, logger, 0)
		case err := <-served:
			logger.Error("HTTP server stopped", "error", err)
			return shutdown(srv, st, stopWorkers, logger, 1)
		case <-tick.C:
			if st.Replication().LeaseLost {
				// Another task writes to the database: stop now, without writing.
				logger.Error("database taken by another task, stopping")
				return shutdown(srv, st, stopWorkers, logger, 1)
			}
		}
	}
}

// startWorkers warms the LibreOffice workers in the background: the console
// works meanwhile, and the try routes answer "starting". The returned
// function waits for the start, then stops the workers.
func startWorkers(ctx context.Context, cfg *settings.Service, api *httpapi.Server, logger *slog.Logger) func() {
	started := make(chan []*render.Pool, 1)
	workers := cfg.Int("render_workers")
	if workers <= 0 {
		cpus := httpapi.TaskCPUs(ctx)
		if cpus <= 0 {
			cpus = float64(runtime.GOMAXPROCS(0))
		}
		workers = min(max(int(math.Ceil(cpus)), 1), 4)
	}
	go func() {
		pool, err := render.Start(ctx, render.Options{
			Command:      workerCommand(),
			Workers:      workers,
			QueueSize:    cfg.Int("render_queue_size"),
			Timeout:      time.Duration(cfg.Int("render_timeout_seconds")) * time.Second,
			RecycleAfter: cfg.Int("worker_recycle_after"),
			Logger:       logger,
		})
		if err != nil {
			if ctx.Err() == nil {
				logger.Error("LibreOffice workers did not start: no document can be produced", "error", err)
			}
			started <- nil
			return
		}
		api.SetRenderer(pool)
		logger.Info("LibreOffice workers ready", "workers", workers)
		pools := []*render.Pool{pool}
		nativeWorkers := max(cfg.Int("native_workers"), 1)
		var nativeEnv []string
		if os.Getenv("PYTHONPATH") == "" {
			nativeEnv = []string{"PYTHONPATH=/usr/local/lib/docibis/python"}
		}
		native, err := render.Start(ctx, render.Options{
			Command:      nativeCommand(),
			Env:          nativeEnv,
			Workers:      nativeWorkers,
			QueueSize:    cfg.Int("render_queue_size"),
			Timeout:      time.Duration(cfg.Int("render_timeout_seconds")) * time.Second,
			StartTimeout: 30 * time.Second,
			Logger:       logger.With("engine", "typst"),
		})
		if err != nil {
			logger.Warn("native engine not started: LibreOffice produces every document", "error", err)
		} else {
			api.SetNative(native)
			pools = append(pools, native)
			logger.Info("native engine ready", "workers", nativeWorkers)
		}
		go api.Maintain(ctx)
		api.StartAsync(ctx, cfg.Int("async_workers"))
		started <- pools
	}()
	return func() {
		api.StopAsync()
		for _, pool := range <-started {
			pool.Close()
		}
	}
}

// shutdown lets running requests finish, stops the workers, then closes the
// database, which flushes its copy and releases the lock. ECS waits 30 s
// before killing.
func shutdown(srv *http.Server, st *store.Store, stopWorkers func(), logger *slog.Logger, code int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Warn("HTTP shutdown", "error", err)
	}
	if stopWorkers != nil {
		stopWorkers()
	}
	if st != nil {
		if err := st.Close(ctx); err != nil {
			logger.Error("database close", "error", err)
			code = 1
		}
	}
	logger.Info("DocIbis stopped")
	return code
}

// healthcheck calls /healthz (alive) or /readyz (ready) on the local server.
func healthcheck(ready bool) int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	host := cfg.Listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	path := "/healthz"
	if ready {
		path = "/readyz"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + host + path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, path, resp.Status)
		return 1
	}
	return 0
}
