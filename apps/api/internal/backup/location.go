package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/benbjohnson/litestream"
	lss3 "github.com/benbjohnson/litestream/s3"

	"docibis/internal/store"
)

// Item is a file of a location.
type Item struct {
	Name     string
	Size     int64
	Modified time.Time
}

// Location keeps the backups: a folder, or a prefix in an S3 bucket.
type Location interface {
	Put(ctx context.Context, name string, r io.ReadSeeker, size int64) error
	Get(ctx context.Context, name string) (io.ReadCloser, error)
	List(ctx context.Context) ([]Item, error)
	Delete(ctx context.Context, name string) error
	// String shows where, without credentials.
	String() string
}

// ErrNotFound means the location has no such file.
var ErrNotFound = errors.New("no such backup")

// LocationFor puts the backups next to the continuous copy: the backups/
// folder of its bucket or directory, or of the data directory without copy.
func LocationFor(ctx context.Context, stateURL, dataDir string) (Location, error) {
	switch {
	case strings.HasPrefix(stateURL, "s3://"):
		c, err := litestream.NewReplicaClientFromURL(stateURL)
		if err != nil {
			return nil, err
		}
		sc, ok := c.(*lss3.ReplicaClient)
		if !ok {
			return nil, fmt.Errorf("state_url %s: not an S3 address", stateURL)
		}
		client, err := store.S3Client(ctx, sc)
		if err != nil {
			return nil, err
		}
		prefix := strings.Trim(path.Join(strings.Trim(sc.Path, "/"), "backups"), "/") + "/"
		return &bucket{client: client, bucket: sc.Bucket, prefix: prefix}, nil
	case strings.HasPrefix(stateURL, "file://"):
		return Dir(filepath.Join(strings.TrimPrefix(stateURL, "file://"), "backups")), nil
	case strings.HasPrefix(stateURL, "/"):
		return Dir(filepath.Join(stateURL, "backups")), nil
	}
	return Dir(filepath.Join(dataDir, "backups")), nil
}

// Dir is a folder of backups.
type Dir string

func (d Dir) String() string { return string(d) }

func (d Dir) file(name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid backup name %q", name)
	}
	return filepath.Join(string(d), name), nil
}

func (d Dir) Put(_ context.Context, name string, r io.ReadSeeker, _ int64) error {
	p, err := d.file(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(string(d), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(string(d), ".put-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (d Dir) Get(_ context.Context, name string) (io.ReadCloser, error) {
	p, err := d.file(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	return f, err
}

func (d Dir) List(context.Context) ([]Item, error) {
	entries, err := os.ReadDir(string(d))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Item
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Item{Name: e.Name(), Size: info.Size(), Modified: info.ModTime()})
	}
	return out, nil
}

func (d Dir) Delete(_ context.Context, name string) error {
	p, err := d.file(name)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

type bucket struct {
	client *s3.Client
	bucket string
	prefix string
}

func (b *bucket) String() string { return "s3://" + b.bucket + "/" + b.prefix }

func (b *bucket) key(name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid backup name %q", name)
	}
	return b.prefix + name, nil
}

func (b *bucket) Put(ctx context.Context, name string, r io.ReadSeeker, size int64) error {
	key, err := b.key(name)
	if err != nil {
		return err
	}
	_, err = b.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &b.bucket, Key: &key, Body: r, ContentLength: aws.Int64(size)})
	return err
}

func (b *bucket) Get(ctx context.Context, name string) (io.ReadCloser, error) {
	key, err := b.key(name)
	if err != nil {
		return nil, err
	}
	out, err := b.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &b.bucket, Key: &key})
	var missing *types.NoSuchKey
	if errors.As(err, &missing) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return out.Body, nil
}

func (b *bucket) List(ctx context.Context) ([]Item, error) {
	var out []Item
	pages := s3.NewListObjectsV2Paginator(b.client, &s3.ListObjectsV2Input{Bucket: &b.bucket, Prefix: &b.prefix})
	for pages.HasMorePages() {
		page, err := pages.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, o := range page.Contents {
			name := strings.TrimPrefix(aws.ToString(o.Key), b.prefix)
			if name == "" || strings.Contains(name, "/") {
				continue
			}
			out = append(out, Item{Name: name, Size: aws.ToInt64(o.Size), Modified: aws.ToTime(o.LastModified)})
		}
	}
	return out, nil
}

func (b *bucket) Delete(ctx context.Context, name string) error {
	key, err := b.key(name)
	if err != nil {
		return err
	}
	_, err = b.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &b.bucket, Key: &key})
	return err
}
