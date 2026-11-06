package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/benbjohnson/litestream"
	_ "github.com/benbjohnson/litestream/file" // file:// copies
	lss3 "github.com/benbjohnson/litestream/s3"
)

// replica is the Litestream copy of the SQLite file.
type replica struct {
	db    *litestream.DB
	store *litestream.Store
	label string
	lease *leaseHolder
}

func startReplica(ctx context.Context, path, replicaURL string, logger *slog.Logger) (*replica, error) {
	label := redactURL(replicaURL)
	client, err := litestream.NewReplicaClientFromURL(replicaURL)
	if err != nil {
		return nil, fmt.Errorf("database copy %s: %w", label, err)
	}
	r := &replica{label: label}
	if sc, ok := client.(*lss3.ReplicaClient); ok {
		leaser, err := newS3Leaser(ctx, sc, logger)
		if err != nil {
			return nil, fmt.Errorf("database copy %s: %w", label, err)
		}
		if r.lease, err = acquireLease(ctx, leaser, leaseWait, logger); err != nil {
			return nil, fmt.Errorf("database copy %s: %w", label, err)
		}
	}
	r.db = litestream.NewDB(path)
	r.db.Replica = litestream.NewReplicaWithClient(r.db, client)
	_, statErr := os.Stat(path)
	if err := r.db.EnsureExists(ctx); err != nil {
		r.lease.release(ctx)
		return nil, fmt.Errorf("restore database from %s: %w", label, err)
	}
	if err := prepareLocalState(ctx, r.db, label, os.IsNotExist(statErr)); err != nil {
		r.lease.release(ctx)
		return nil, fmt.Errorf("database copy %s: %w", label, err)
	}
	r.store = litestream.NewStore([]*litestream.DB{r.db}, litestream.CompactionLevels{
		{Level: 0},
		{Level: 1, Interval: 10 * time.Second},
		{Level: 2, Interval: 5 * time.Minute},
	})
	// A snapshot every 6 hours keeps restores short after a task replacement.
	r.store.SnapshotInterval = 6 * time.Hour
	r.store.Logger = logger.With("component", "litestream")
	if err := r.store.Open(ctx); err != nil {
		r.lease.release(ctx)
		return nil, fmt.Errorf("start database copy: %w", err)
	}
	logger.Info("database copy started", "target", label, "locked", r.lease != nil)
	return r, nil
}

func (r *replica) sync(ctx context.Context) error { return r.db.SyncAndWait(ctx) }

func (r *replica) status() Replication {
	st := Replication{Enabled: true, Target: r.label, Locked: r.lease != nil}
	if r.lease != nil {
		st.LeaseLost = r.lease.lost()
	}
	return st
}

func (r *replica) close(ctx context.Context) error {
	err := r.store.Close(ctx)
	r.lease.release(ctx)
	return err
}

// markerName records, next to Litestream's local files, the copy they belong to.
const markerName = "docibis-replica"

// prepareLocalState drops Litestream's local files when they cannot continue
// the copy: another destination, a new or restored database file, or a copy
// that lost its latest files. Without this, Litestream retries forever a file
// it no longer has (seen with v0.5.17).
func prepareLocalState(ctx context.Context, ls *litestream.DB, label string, fresh bool) error {
	top, err := ls.Replica.MaxLTXFileInfo(ctx, 0)
	if err != nil {
		return fmt.Errorf("read the copy: %w", err)
	}
	for _, level := range []int{1, 2, litestream.SnapshotLevel} {
		info, err := ls.Replica.MaxLTXFileInfo(ctx, level)
		if err != nil {
			return fmt.Errorf("read the copy: %w", err)
		}
		if info.MaxTXID > top.MaxTXID {
			return fmt.Errorf("the destination holds an incomplete copy (level %d reaches transaction %s, level 0 %s): choose an empty location",
				level, info.MaxTXID, top.MaxTXID)
		}
	}
	marker := filepath.Join(ls.MetaPath(), markerName)
	prev, _ := os.ReadFile(marker)
	reset := fresh || string(prev) != label
	if !reset {
		_, local, err := ls.MaxLTX()
		if err != nil {
			return err
		}
		next := top.MaxTXID + 1
		if local >= next {
			if _, err := os.Stat(ls.LTXPath(0, next, next)); os.IsNotExist(err) {
				reset = true
			}
		}
	}
	if reset {
		if _, err := os.Stat(ls.LTXDir()); err == nil {
			if err := ls.ResetLocalState(ctx); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(ls.MetaPath(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(marker, []byte(label), 0o600)
}

func newS3Leaser(ctx context.Context, sc *lss3.ReplicaClient, logger *slog.Logger) (litestream.Leaser, error) {
	client, err := S3Client(ctx, sc)
	if err != nil {
		return nil, err
	}
	l := lss3.NewLeaser()
	l.Bucket, l.Path = sc.Bucket, sc.Path
	l.SetClient(client)
	l.SetLogger(logger)
	return l, nil
}

// S3Client reaches the bucket of a copy with the credentials of the
// environment (the task role on AWS).
func S3Client(ctx context.Context, sc *lss3.ReplicaClient) (*s3.Client, error) {
	region := sc.Region
	if region == "" {
		region = os.Getenv("AWS_REGION")
	}
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		if sc.Endpoint != "" {
			o.BaseEndpoint = aws.String(sc.Endpoint)
			o.UsePathStyle = true
		}
	}), nil
}

// leaseWait bounds the wait for a lease left by a task that stopped without
// releasing it (Litestream's lease lasts 30 s). Tests shorten it.
var leaseWait = 45 * time.Second

// leaseHolder keeps the S3 lease that makes this task the only writer.
type leaseHolder struct {
	leaser litestream.Leaser
	logger *slog.Logger

	mu      sync.Mutex
	lease   *litestream.Lease
	isLost  bool
	stop    chan struct{}
	stopped chan struct{}
}

// acquireLease takes the lease, waiting up to maxWait for an expiring one,
// then renews it in the background.
func acquireLease(ctx context.Context, leaser litestream.Leaser, maxWait time.Duration, logger *slog.Logger) (*leaseHolder, error) {
	deadline := time.Now().Add(maxWait)
	for {
		lease, err := leaser.AcquireLease(ctx)
		if err == nil {
			h := &leaseHolder{leaser: leaser, logger: logger, lease: lease, stop: make(chan struct{}), stopped: make(chan struct{})}
			go h.renew()
			return h, nil
		}
		var held *litestream.LeaseExistsError
		if !errors.As(err, &held) {
			return nil, fmt.Errorf("take the lock: %w", err)
		}
		if !time.Now().Before(deadline) {
			return nil, fmt.Errorf("another task writes to this database (%w)", err)
		}
		wait := min(time.Until(held.ExpiresAt)+time.Second, time.Until(deadline))
		logger.Warn("database locked by another task, waiting", "owner", held.Owner, "until", held.ExpiresAt)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(max(wait, 100*time.Millisecond)):
		}
	}
}

func (h *leaseHolder) renew() {
	defer close(h.stopped)
	ticker := time.NewTicker(leaseRenewEvery(h.current()))
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		next, err := h.leaser.RenewLease(ctx, h.current())
		cancel()
		h.mu.Lock()
		switch {
		case err == nil:
			h.lease = next
		case errors.Is(err, litestream.ErrLeaseNotHeld):
			h.isLost = true
			h.logger.Error("database lock lost: another task took the database")
		default:
			h.logger.Warn("database lock renewal failed, retrying", "error", err)
		}
		lost := h.isLost
		h.mu.Unlock()
		if lost {
			return
		}
	}
}

func leaseRenewEvery(l *litestream.Lease) time.Duration {
	return max(l.TTL()/3, 100*time.Millisecond)
}

func (h *leaseHolder) current() *litestream.Lease {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lease
}

func (h *leaseHolder) lost() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.isLost
}

// release stops the renewal and frees the lease; safe on a nil holder.
func (h *leaseHolder) release(ctx context.Context) {
	if h == nil {
		return
	}
	close(h.stop)
	<-h.stopped
	if h.lost() {
		return
	}
	if err := h.leaser.ReleaseLease(ctx, h.current()); err != nil {
		h.logger.Warn("database lock release failed; it expires by itself", "error", err)
	}
}

// redactURL keeps a copy's address without credentials or query parameters.
func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(invalid address)"
	}
	u.User, u.RawQuery = nil, ""
	return u.String()
}
