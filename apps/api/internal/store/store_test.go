package store

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/benbjohnson/litestream"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestSQLiteRestoresFromItsCopy(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	opts := Options{Path: filepath.Join(dir, "data", "docibis.db"), ReplicaURL: "file://" + filepath.Join(dir, "copy")}

	s, err := Open(ctx, opts, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if s.Engine() != SQLite || !s.Replication().Enabled {
		t.Fatalf("engine %s, replication %+v", s.Engine(), s.Replication())
	}
	if err := s.SetSetting(ctx, "instance_name", "INT"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	// A new task starts with an empty disk: everything comes back from the copy.
	if err := os.RemoveAll(filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, opts, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	got, ok, err := s.Setting(ctx, "instance_name")
	if err != nil || !ok || got != "INT" {
		t.Fatalf("after restore: %q %v %v", got, ok, err)
	}
	if err := s.SetSetting(ctx, "instance_name", "REC"); err != nil {
		t.Fatalf("the copy must continue after a restore: %v", err)
	}
}

func TestSQLiteWithoutCopy(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	if s.Replication().Enabled {
		t.Fatal("no copy expected")
	}
	if _, ok, err := s.Setting(ctx, "missing"); ok || err != nil {
		t.Fatalf("missing setting: %v %v", ok, err)
	}
}

// TestPostgres runs when DOCIBIS_TEST_POSTGRES_URL points to a server (CI).
func TestPostgres(t *testing.T) {
	base := os.Getenv("DOCIBIS_TEST_POSTGRES_URL")
	if base == "" {
		t.Skip("DOCIBIS_TEST_POSTGRES_URL not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("t_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer admin.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")

	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	s, err := Open(ctx, Options{URL: base + sep + "search_path=" + schema}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	if s.Engine() != Postgres || s.Replication().Enabled {
		t.Fatalf("engine %s, replication %+v", s.Engine(), s.Replication())
	}
	for _, v := range []string{"INT", "REC"} {
		if err := s.SetSetting(ctx, "instance_name", v); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.Setting(ctx, "instance_name")
	if err != nil || !ok || got != "REC" {
		t.Fatalf("got %q %v %v", got, ok, err)
	}
	checkAPIKeys(t, s)
	checkHistory(t, s)
	checkTemplates(t, s)
	checkAliases(t, s)
	checkSpaces(t, s)
	checkRenderJobs(t, s)
}

// fakeLease is one lock object shared by several fakeLeasers, like lock.json on S3.
type fakeLease struct {
	mu      sync.Mutex
	owner   string
	expires time.Time
	gen     int64
}

type fakeLeaser struct {
	obj   *fakeLease
	owner string
	ttl   time.Duration
}

func (f *fakeLeaser) Type() string { return "fake" }

func (f *fakeLeaser) AcquireLease(context.Context) (*litestream.Lease, error) {
	f.obj.mu.Lock()
	defer f.obj.mu.Unlock()
	if f.obj.owner != "" && time.Now().Before(f.obj.expires) {
		return nil, &litestream.LeaseExistsError{Owner: f.obj.owner, ExpiresAt: f.obj.expires}
	}
	f.obj.gen++
	f.obj.owner, f.obj.expires = f.owner, time.Now().Add(f.ttl)
	return &litestream.Lease{Generation: f.obj.gen, Owner: f.owner, ExpiresAt: f.obj.expires, ETag: fmt.Sprint(f.obj.gen)}, nil
}

func (f *fakeLeaser) RenewLease(_ context.Context, l *litestream.Lease) (*litestream.Lease, error) {
	f.obj.mu.Lock()
	defer f.obj.mu.Unlock()
	if f.obj.owner != f.owner || f.obj.gen != l.Generation {
		return nil, litestream.ErrLeaseNotHeld
	}
	f.obj.expires = time.Now().Add(f.ttl)
	return &litestream.Lease{Generation: l.Generation, Owner: f.owner, ExpiresAt: f.obj.expires, ETag: l.ETag}, nil
}

func (f *fakeLeaser) ReleaseLease(_ context.Context, l *litestream.Lease) error {
	f.obj.mu.Lock()
	defer f.obj.mu.Unlock()
	if f.obj.owner != f.owner || f.obj.gen != l.Generation {
		return litestream.ErrLeaseNotHeld
	}
	f.obj.owner = ""
	return nil
}

func TestLeaseSingleWriter(t *testing.T) {
	ctx := context.Background()
	obj := &fakeLease{}
	a := &fakeLeaser{obj: obj, owner: "task-a", ttl: 300 * time.Millisecond}
	b := &fakeLeaser{obj: obj, owner: "task-b", ttl: 300 * time.Millisecond}

	ha, err := acquireLease(ctx, a, time.Second, quiet)
	if err != nil {
		t.Fatal(err)
	}
	// A is alive and renews: B gives up after its wait.
	if _, err := acquireLease(ctx, b, 500*time.Millisecond, quiet); err == nil {
		t.Fatal("second writer accepted while the first one is alive")
	}
	if ha.lost() {
		t.Fatal("A must still hold the lease")
	}
	ha.release(ctx)
	hb, err := acquireLease(ctx, b, time.Second, quiet)
	if err != nil {
		t.Fatalf("B must take the released lease: %v", err)
	}
	hb.release(ctx)
}

func TestLeaseLeftByACrashedTask(t *testing.T) {
	ctx := context.Background()
	obj := &fakeLease{owner: "crashed", expires: time.Now().Add(200 * time.Millisecond)}
	b := &fakeLeaser{obj: obj, owner: "task-b", ttl: time.Second}
	start := time.Now()
	h, err := acquireLease(ctx, b, 2*time.Second, quiet)
	if err != nil {
		t.Fatalf("B must wait for the expiry, then take the lease: %v", err)
	}
	defer h.release(ctx)
	if time.Since(start) < 150*time.Millisecond {
		t.Fatal("B took the lease before it expired")
	}
}

func TestLeaseLost(t *testing.T) {
	ctx := context.Background()
	obj := &fakeLease{}
	a := &fakeLeaser{obj: obj, owner: "task-a", ttl: 300 * time.Millisecond}
	h, err := acquireLease(ctx, a, time.Second, quiet)
	if err != nil {
		t.Fatal(err)
	}
	obj.mu.Lock()
	obj.owner, obj.gen = "intruder", obj.gen+1
	obj.mu.Unlock()
	deadline := time.Now().Add(2 * time.Second)
	for !h.lost() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !h.lost() {
		t.Fatal("the renewal must notice that another task took the lease")
	}
	h.release(ctx)
}

func TestRedactURL(t *testing.T) {
	if got := redactURL("s3://key:secret@bucket/db?region=eu-central-1&endpoint=x"); got != "s3://bucket/db" {
		t.Fatalf("got %q", got)
	}
}

// TestS3 runs against a real bucket when DOCIBIS_TEST_S3_URL is set
// (s3://bucket/prefix, empty prefix, AWS credentials in the environment):
// one writer at a time, then a new task restores the data from S3.
func TestS3(t *testing.T) {
	base := os.Getenv("DOCIBIS_TEST_S3_URL")
	if base == "" {
		t.Skip("DOCIBIS_TEST_S3_URL not set")
	}
	ctx := context.Background()
	replica := strings.TrimSuffix(base, "/") + fmt.Sprintf("/t%d/db", time.Now().UnixNano())
	first := Options{Path: filepath.Join(t.TempDir(), "docibis.db"), ReplicaURL: replica}
	second := Options{Path: filepath.Join(t.TempDir(), "docibis.db"), ReplicaURL: replica}

	a, err := Open(ctx, first, quiet)
	if err != nil {
		t.Fatal(err)
	}
	if r := a.Replication(); !r.Locked {
		t.Fatalf("an S3 copy must be locked: %+v", r)
	}
	if err := a.SetSetting(ctx, "instance_name", "INT"); err != nil {
		t.Fatal(err)
	}

	defer func(d time.Duration) { leaseWait = d }(leaseWait)
	leaseWait = 3 * time.Second
	if b, err := Open(ctx, second, quiet); err == nil {
		b.Close(ctx)
		t.Fatal("a second task opened the database while the first one writes")
	}

	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := Open(ctx, second, quiet)
	if err != nil {
		t.Fatalf("the lock must be free once the first task stopped: %v", err)
	}
	defer b.Close(ctx)
	got, ok, err := b.Setting(ctx, "instance_name")
	if err != nil || !ok || got != "INT" {
		t.Fatalf("restored from S3: %q %v %v", got, ok, err)
	}
}
