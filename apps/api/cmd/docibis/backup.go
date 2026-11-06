package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"docibis/internal/backup"
	"docibis/internal/config"
	"docibis/internal/httpapi"
	"docibis/internal/store"
)

var errDataLocked = errors.New("another DocIbis process uses this data directory")

// lockData keeps other DocIbis processes off the data directory (the SQLite
// file and its copy) until release; released anyway when the process ends.
func lockData(dir string) (release func(), err error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "docibis.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errDataLocked
		}
		return nil, err
	}
	return func() { f.Close() }, nil
}

// parseAnywhere accepts the flags before or after the other arguments.
func parseAnywhere(fs *flag.FlagSet, args []string) error {
	var flags, rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			rest = append(rest, a)
		}
	}
	return fs.Parse(append(flags, rest...))
}

func openStore(ctx context.Context, cfg config.Config) (*store.Store, backup.Location, error) {
	loc, err := backup.LocationFor(ctx, cfg.StateURL, cfg.DataDir)
	if err != nil {
		return nil, nil, fmt.Errorf("backup location: %w", err)
	}
	replicaURL, _ := cfg.ReplicaURL()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	st, err := store.Open(ctx, store.Options{Path: cfg.SQLitePath(), URL: cfg.DatabaseURL, ReplicaURL: replicaURL}, logger)
	return st, loc, err
}

func instanceName(ctx context.Context, st *store.Store, cfg config.Config) string {
	if v, ok, _ := st.Setting(ctx, "instance_name"); ok && v != "" {
		return v
	}
	return cfg.InstanceName
}

func describe(b backup.Info) string {
	out := fmt.Sprintf("%s, %.1f MB", b.Name, float64(b.Size)/(1<<20))
	if b.Summary != nil {
		var rows int64
		for _, n := range b.Summary.Rows {
			rows += n
		}
		out += fmt.Sprintf(", %d rows, DocIbis %s, %s", rows, b.Summary.Version, b.Summary.Engine)
	}
	if b.Instance != "" {
		out += ", instance " + b.Instance
	}
	return out
}

// backupCmd makes a backup: through the service when it runs in this
// container, else with the database opened here.
func backupCmd(args []string) int {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	kind := fs.String("kind", backup.Manual, "manual, or update (before an update)")
	if err := parseAnywhere(fs, args); err != nil || fs.NArg() > 0 || (*kind != backup.Manual && *kind != backup.Update) {
		fmt.Fprintln(os.Stderr, "usage: docibis backup [--kind manual|update]")
		return 2
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if info, asked, err := askService(ctx, cfg, *kind); asked {
		if err != nil {
			fmt.Fprintln(os.Stderr, "backup failed:", err)
			return 1
		}
		report(info)
		return 0
	}
	release, err := lockData(cfg.DataDir)
	if errors.Is(err, errDataLocked) {
		fmt.Fprintln(os.Stderr, "DocIbis runs in another container: run the command in it (docker compose exec api docibis backup)")
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer release()
	st, loc, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer st.Close(context.WithoutCancel(ctx))
	info, err := backup.Create(ctx, st, loc, backup.Secret(), *kind, instanceName(ctx, st, cfg), cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "backup failed:", err)
		return 1
	}
	report(info)
	fmt.Println("Location:", loc)
	return 0
}

func report(info backup.Info) {
	fmt.Println("Backup made:", describe(info))
	if !info.Verified {
		fmt.Println("Not verified:", info.Problem)
	}
}

// askService asks the service of this container for the backup, with the
// token it wrote in the data directory; asked is false when it does not run here.
func askService(ctx context.Context, cfg config.Config, kind string) (info backup.Info, asked bool, err error) {
	token, err := os.ReadFile(filepath.Join(cfg.DataDir, httpapi.LocalTokenFile))
	if err != nil {
		return info, false, nil
	}
	host := cfg.Listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+host+"/local/backup?kind="+kind, nil)
	if err != nil {
		return info, false, nil
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	client := http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 2 * time.Hour}
	resp, err := client.Do(req)
	if err != nil {
		return info, false, nil
	}
	defer resp.Body.Close()
	var body struct {
		backup.Info
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal(raw, &body)
		if body.Error == "" {
			body.Error = resp.Status
		}
		return info, true, errors.New(body.Error)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return info, true, err
	}
	return body.Info, true, nil
}

func backupsCmd(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "usage: docibis backups")
		return 2
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx := context.Background()
	loc, err := backup.LocationFor(ctx, cfg.StateURL, cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	all, err := backup.List(ctx, loc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSIZE\tDOCIBIS\tENGINE\tVERIFIED")
	for _, b := range all {
		b = backup.Describe(ctx, loc, b)
		version, engine, verified := "?", "?", "no"
		if b.Summary != nil {
			version, engine = b.Summary.Version, string(b.Summary.Engine)
		}
		if b.Verified {
			verified = "yes"
		}
		fmt.Fprintf(w, "%s\t%.1f MB\t%s\t%s\t%s\n", b.Name, float64(b.Size)/(1<<20), version, engine, verified)
	}
	w.Flush()
	fmt.Printf("%d backup(s) in %s\n", len(all), loc)
	return 0
}

// restoreCmd replaces the database with a backup, the service stopped: the
// state before is backed up first.
func restoreCmd(args []string) int {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "also drop the tables this version does not know (left by a later version)")
	yes := fs.Bool("yes", false, "do not ask for a confirmation")
	if err := parseAnywhere(fs, args); err != nil || fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: docibis restore NAME|latest|YYYY-MM-DD|FILE [--yes] [--force]")
		return 2
	}
	what := fs.Arg(0)
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	secret := backup.Secret()
	if secret == nil {
		fmt.Fprintln(os.Stderr, backup.ErrNoKey)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	release, err := lockData(cfg.DataDir)
	if errors.Is(err, errDataLocked) {
		fmt.Fprintln(os.Stderr, "DocIbis is running: stop it first (docker compose stop api), then: docker compose run --rm api restore", what)
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer release()
	st, loc, err := openStore(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer st.Close(context.WithoutCancel(ctx))
	work, err := os.MkdirTemp(cfg.DataDir, ".restore-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(work)

	file, chosen := what, backup.Info{Name: filepath.Base(what)}
	if stat, err := os.Stat(what); err != nil || stat.IsDir() {
		all, err := backup.List(ctx, loc)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		b, ok := backup.Find(all, what)
		if !ok {
			fmt.Fprintf(os.Stderr, "no backup %q in %s (docibis backups lists them)\n", what, loc)
			return 1
		}
		chosen = backup.Describe(ctx, loc, b)
		if file, err = backup.Fetch(ctx, loc, b.Name, work); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	fmt.Println("Backup:  ", describe(chosen))
	fmt.Println("Database:", st.Engine(), "instance", instanceName(ctx, st, cfg))
	if !*yes && !confirm("Replace the whole database with this backup? Its current state is backed up first. [y/N] ") {
		fmt.Println("Nothing changed (--yes skips this question).")
		return 1
	}
	safety, err := backup.Create(ctx, st, loc, secret, backup.BeforeRestore, instanceName(ctx, st, cfg), cfg.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "the current state could not be backed up, nothing was restored:", err)
		return 1
	}
	fmt.Println("Current state backed up:", safety.Name)
	sum, err := backup.Restore(ctx, st, file, secret, *force)
	if err != nil {
		if errors.Is(err, store.ErrUnknownTables) {
			fmt.Fprintln(os.Stderr, "restore refused:", err, "(--force drops them)")
		} else {
			fmt.Fprintln(os.Stderr, "restore failed:", err)
		}
		fmt.Fprintln(os.Stderr, "To come back to the state before: docibis restore", safety.Name)
		return 1
	}
	var rows int64
	for _, n := range sum.Rows {
		rows += n
	}
	// Sessions closed since the backup must not come back.
	if _, err := st.DB.ExecContext(ctx, "DELETE FROM sessions"); err != nil {
		fmt.Fprintln(os.Stderr, "sessions not closed:", err)
	}
	fmt.Printf("Restored: %d rows in %d tables, made by DocIbis %s on %s. Everyone signs in again. Start DocIbis again.\n", rows, len(sum.Rows), sum.Version, sum.Created.Format(time.DateTime))
	return 0
}

func confirm(question string) bool {
	fmt.Print(question)
	answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	answer = strings.ToLower(strings.TrimSpace(answer))
	return answer == "y" || answer == "yes" || answer == "o" || answer == "oui"
}
