package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v5"

	"docibis/internal/backup"
)

// Backups of the database: every night at the hour set, and when an
// administrator asks, next to the continuous copy (internal/backup). A
// restore needs the service stopped: it is the docibis restore command, not
// a button.

// LocalTokenFile, in the data directory, lets the docibis command of the same
// machine ask the running service for a backup (POST /local/backup).
const LocalTokenFile = ".local-token"

const localBackupPath = "/local/backup"

var errBackupRunning = errors.New("a backup is already running")

type backupState struct {
	mu      sync.Mutex
	loc     backup.Location
	work    string
	token   string
	running bool
	lastErr string
	day     string
	// notes keeps what the note of each backup says, read once.
	notes sync.Map
}

// SetBackups says where the backups go and the folder they are made in, and
// writes the token of the local command.
func (s *Server) SetBackups(loc backup.Location, dataDir string) {
	old, _ := filepath.Glob(filepath.Join(dataDir, ".backup-*"))
	for _, dir := range old {
		_ = os.RemoveAll(dir)
	}
	token := newToken()
	if err := os.WriteFile(filepath.Join(dataDir, LocalTokenFile), []byte(token), 0o600); err != nil {
		s.logger.Warn("docibis backup cannot reach this service: token not written", "error", err)
		token = ""
	}
	s.backups.mu.Lock()
	defer s.backups.mu.Unlock()
	s.backups.loc, s.backups.work, s.backups.token = loc, dataDir, token
}

// runBackup makes a backup, then deletes those past their time; one at a time.
func (s *Server) runBackup(ctx context.Context, kind string) (backup.Info, error) {
	b := &s.backups
	b.mu.Lock()
	if b.running {
		b.mu.Unlock()
		return backup.Info{}, errBackupRunning
	}
	b.running = true
	loc, work := b.loc, b.work
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.running = false
		b.mu.Unlock()
	}()
	st, cfg := s.db.Load(), s.cfg.Load()
	if loc == nil || st == nil {
		return backup.Info{}, errors.New("backups not ready")
	}
	start := time.Now()
	info, err := backup.Create(ctx, st, loc, backup.Secret(), kind, cfg.String("instance_name"), work)
	b.mu.Lock()
	b.lastErr = ""
	if err != nil {
		b.lastErr = err.Error()
	}
	b.mu.Unlock()
	if err != nil {
		s.logger.Error("backup failed", "kind", kind, "error", err)
		return info, err
	}
	b.notes.Store(info.Name, info)
	attrs := []any{"name", info.Name, "kind", kind, "size", info.Size, "duration_ms", time.Since(start).Milliseconds()}
	if info.Verified {
		s.logger.Info("backup made", attrs...)
	} else {
		s.logger.Error("backup made but not verified", append(attrs, "problem", info.Problem)...)
	}
	gone, err := backup.Prune(ctx, loc, cfg.Int("backup_retention_days"), time.Now())
	if err != nil {
		s.logger.Warn("old backups not deleted", "error", err)
	}
	for _, name := range gone {
		b.notes.Delete(name)
	}
	if len(gone) > 0 {
		s.logger.Info("old backups deleted", "backups", len(gone))
	}
	return info, nil
}

// WatchBackups makes the backup of the night once its hour has come, on one
// task only: the day is claimed in the database.
func (s *Server) WatchBackups(ctx context.Context) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		s.nightly(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) nightly(ctx context.Context) {
	st, cfg := s.db.Load(), s.cfg.Load()
	if st == nil || cfg == nil || cfg.String("backup_enabled") != "true" || backup.Secret() == nil || st.Replication().LeaseLost {
		return
	}
	now := time.Now().UTC()
	day := now.Format(time.DateOnly)
	s.backups.mu.Lock()
	ready, done := s.backups.loc != nil, s.backups.day == day
	s.backups.mu.Unlock()
	if !ready || done || now.Hour() < cfg.Int("backup_hour") {
		return
	}
	mine, err := st.ClaimDay(ctx, "backup.nightly", day)
	if err != nil {
		s.logger.Warn("nightly backup not started", "error", err)
		return
	}
	s.backups.mu.Lock()
	s.backups.day = day
	s.backups.mu.Unlock()
	if mine {
		_, _ = s.runBackup(ctx, backup.Nightly)
	}
}

func (s *Server) backupsOut(ctx context.Context) Backups {
	cfg := s.cfg.Load()
	b := &s.backups
	b.mu.Lock()
	loc, running, lastErr := b.loc, b.running, b.lastErr
	b.mu.Unlock()
	out := Backups{
		Enabled:       cfg.String("backup_enabled") == "true",
		Hour:          cfg.Int("backup_hour"),
		RetentionDays: cfg.Int("backup_retention_days"),
		KeyMissing:    backup.Secret() == nil,
		Running:       running,
		Backups:       []Backup{},
	}
	if loc == nil {
		return out
	}
	out.Location = loc.String()
	list, err := backup.List(ctx, loc)
	if err != nil {
		s.logger.Warn("backups not listed", "error", err)
		lastErr = "Liste illisible : " + err.Error()
	}
	if lastErr != "" {
		out.LastError = &lastErr
	}
	for _, info := range list {
		if note, ok := b.notes.Load(info.Name); ok {
			info = note.(backup.Info)
		} else {
			info = backup.Describe(ctx, loc, info)
			b.notes.Store(info.Name, info)
		}
		out.Backups = append(out.Backups, backupOut(info))
	}
	return out
}

func backupOut(b backup.Info) Backup {
	out := Backup{Name: b.Name, Kind: BackupKind(b.Kind), CreatedAt: b.Created, Size: b.Size, Verified: b.Verified}
	if b.Problem != "" {
		out.Problem = &b.Problem
	}
	if sum := b.Summary; sum != nil {
		engine := BackupEngine(sum.Engine)
		out.Version, out.Engine, out.Schema, out.Rows = &sum.Version, &engine, &sum.Schema, &sum.Rows
	}
	return out
}

func (s *Server) ListBackups(ctx context.Context, _ ListBackupsRequestObject) (ListBackupsResponseObject, error) {
	return ListBackups200JSONResponse(s.backupsOut(ctx)), nil
}

func (s *Server) CreateBackup(ctx context.Context, _ CreateBackupRequestObject) (CreateBackupResponseObject, error) {
	if backup.Secret() == nil {
		return CreateBackup400ApplicationProblemPlusJSONResponse{badRequest("Les sauvegardes sont chiffrées avec APP_SECRET_KEY (32 caractères au moins) : définissez cette variable, puis redémarrez DocIbis.")}, nil
	}
	me, _ := userFrom(ctx)
	bg := context.WithoutCancel(ctx)
	go func() {
		if info, err := s.runBackup(bg, backup.Manual); err == nil {
			s.audit(bg, me.Username, "backup.create", "backup:"+info.Name, "")
		}
	}()
	out := s.backupsOut(ctx)
	out.Running = true
	return CreateBackup202JSONResponse(out), nil
}

func (s *Server) DownloadBackup(ctx context.Context, req DownloadBackupRequestObject) (DownloadBackupResponseObject, error) {
	notFound := DownloadBackup404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Sauvegarde inconnue."))}
	s.backups.mu.Lock()
	loc := s.backups.loc
	s.backups.mu.Unlock()
	if loc == nil {
		return notFound, nil
	}
	list, err := backup.List(ctx, loc)
	if err != nil {
		return nil, err
	}
	var found *backup.Info
	for i := range list {
		if list[i].Name == req.Name {
			found = &list[i]
		}
	}
	if found == nil {
		return notFound, nil
	}
	r, err := loc.Get(ctx, found.Name)
	if errors.Is(err, backup.ErrNotFound) {
		return notFound, nil
	}
	if err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "backup.download", "backup:"+found.Name, "")
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": found.Name})
	return DownloadBackup200ApplicationoctetStreamResponse{Body: r, ContentLength: found.Size, Headers: DownloadBackup200ResponseHeaders{ContentDisposition: &disposition}}, nil
}

// serveLocalBackup makes a backup for the docibis command of the same
// machine, which reads the token in the data directory. CloudFront, the load
// balancer and the front do not route /local.
func (s *Server) serveLocalBackup(c *echo.Context) error {
	r := c.Request()
	s.backups.mu.Lock()
	token := s.backups.token
	s.backups.mu.Unlock()
	got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || r.Header.Get("X-Forwarded-For") != "" || subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
		return echo.NewHTTPError(http.StatusForbidden, "Local token required.")
	}
	if s.db.Load() == nil {
		return echo.NewHTTPError(http.StatusServiceUnavailable, starting)
	}
	kind := backup.Manual
	if c.QueryParam("kind") == backup.Update {
		kind = backup.Update
	}
	info, err := s.runBackup(r.Context(), kind)
	if err != nil {
		return c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	}
	return c.JSON(http.StatusOK, info)
}
