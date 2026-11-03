package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestRenderJobsSQLite(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, Options{Path: filepath.Join(t.TempDir(), "docibis.db")}, quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(ctx)
	checkRenderJobs(t, s)
}

// checkRenderJobs runs on every database engine: the life of a document
// produced in the background, a task that stops, the purge.
func checkRenderJobs(t *testing.T, s *Store) {
	t.Helper()
	ctx := context.Background()
	hour := time.Hour
	j := &RenderJob{ID: "job_a", KeyID: 7, Actor: "portail", Status: "queued", Template: "lettre.docx", Format: "pdf",
		Upload: "pending", Callback: "pending", ExpiresAt: time.Now().Add(hour)}
	if err := s.CreateRenderJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.RenderJob(ctx, "job_a", 8); ok || err != nil {
		t.Fatalf("another key reads the job: %v %v", ok, err)
	}
	if got, ok, err := s.RenderJob(ctx, "job_a", 7); err != nil || !ok || got.Status != "queued" || got.Upload != "pending" || got.Actor != "portail" {
		t.Fatalf("queued: %+v %v %v", got, ok, err)
	}

	j.Status = "running"
	if err := s.UpdateRenderJob(ctx, j, "status"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	j.Status, j.FinishedAt, j.FileName, j.Size, j.Document, j.Upload = "done", &now, "lettre.pdf", 4, []byte("%PDF"), "failed"
	if err := s.UpdateRenderJob(ctx, j); err != nil {
		t.Fatal(err)
	}
	j.Callback, j.Document = "done", nil
	if err := s.UpdateRenderJob(ctx, j, "callback"); err != nil {
		t.Fatal(err)
	}
	full, ok, err := s.RenderJobDocument(ctx, "job_a", 7)
	if err != nil || !ok || string(full.Document) != "%PDF" || full.Callback != "done" || full.Upload != "failed" || full.FinishedAt == nil || full.FileName != "lettre.pdf" {
		t.Fatalf("done: %+v %v %v", full, ok, err)
	}
	if got, _, _ := s.RenderJob(ctx, "job_a", 7); got.Document != nil || got.Size != 4 {
		t.Fatalf("the state alone: %+v", got)
	}

	// A task stops: its jobs fail once silent; those still held stay.
	stale := &RenderJob{ID: "job_b", KeyID: 7, Status: "running", Format: "pdf", Upload: "pending", ExpiresAt: time.Now().Add(hour)}
	held := &RenderJob{ID: "job_c", KeyID: 7, Status: "queued", Format: "pdf", Callback: "pending", ExpiresAt: time.Now().Add(hour)}
	for _, r := range []*RenderJob{stale, held} {
		if err := s.CreateRenderJob(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(20 * time.Millisecond)
	mark := time.Now()
	time.Sleep(20 * time.Millisecond)
	if err := s.TouchRenderJobs(ctx, []string{"job_c"}, hour); err != nil {
		t.Fatal(err)
	}
	if n, err := s.FailStaleRenderJobs(ctx, mark, "Interrompu", hour); n != 1 || err != nil {
		t.Fatalf("stale: %d %v", n, err)
	}
	if got, _, _ := s.RenderJob(ctx, "job_b", 7); got.Status != "failed" || got.Error != "Interrompu" || got.Upload != "failed" || got.FinishedAt == nil {
		t.Fatalf("stopped: %+v", got)
	}
	if got, _, _ := s.RenderJob(ctx, "job_c", 7); got.Status != "queued" || got.Callback != "pending" {
		t.Fatalf("held: %+v", got)
	}
	if got, _, _ := s.RenderJob(ctx, "job_a", 7); got.Status != "done" {
		t.Fatalf("a finished job stays: %+v", got)
	}

	expired := &RenderJob{ID: "job_d", KeyID: 7, Status: "done", Format: "pdf", Document: []byte("%PDF"), ExpiresAt: time.Now().Add(-time.Minute)}
	if err := s.CreateRenderJob(ctx, expired); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.RenderJobDocument(ctx, "job_d", 7); ok {
		t.Fatal("an expired job is gone")
	}
	if n, err := s.PurgeRenderJobs(ctx); n != 1 || err != nil {
		t.Fatalf("purge: %d %v", n, err)
	}
}
