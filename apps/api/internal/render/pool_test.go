package render

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The test binary doubles as a fake worker, so the pool is tested without
// LibreOffice: "hang" never answers, "crash" exits, anything else is copied.
func TestMain(m *testing.M) {
	if os.Getenv("DOCIBIS_FAKE_WORKER") == "1" {
		fakeWorker()
		return
	}
	os.Exit(m.Run())
}

func fakeWorker() {
	out := json.NewEncoder(os.Stdout)
	_ = out.Encode(Reply{Ready: true, Ms: 1})
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var req Request
		_ = json.Unmarshal(in.Bytes(), &req)
		switch filepath.Base(req.In) {
		case "hang":
			time.Sleep(time.Hour)
		case "crash":
			os.Exit(3)
		}
		data, err := os.ReadFile(req.In)
		r := Reply{ID: req.ID, Ms: 2}
		if err == nil && req.Inputs != nil {
			data = append(data, " "+req.Inputs["data"]...)
		}
		if err == nil {
			err = os.WriteFile(req.Out, []byte(strings.ToUpper(string(data))), 0o600)
		}
		if err != nil {
			r.Error = err.Error()
		}
		_ = out.Encode(r)
	}
}

func start(t *testing.T, opts Options) *Pool {
	t.Helper()
	opts.Command = []string{os.Args[0]}
	opts.Env = []string{"DOCIBIS_FAKE_WORKER=1"}
	p, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func convert(t *testing.T, p *Pool, name string) error {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, name)
	if err := os.WriteFile(in, []byte("doc "+name), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.pdf")
	if _, err := p.Convert(context.Background(), in, out, "pdf", ""); err != nil {
		return err
	}
	got, err := os.ReadFile(out)
	if err != nil || string(got) != strings.ToUpper("doc "+name) {
		t.Fatalf("output %q %v", got, err)
	}
	return nil
}

func TestParallelConversions(t *testing.T) {
	p := start(t, Options{Workers: 3, QueueSize: 10, Timeout: 5 * time.Second})
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- convert(t, p, "file"+string(rune('a'+i)))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestHungAndCrashedWorkersAreReplaced(t *testing.T) {
	p := start(t, Options{Workers: 1, QueueSize: 5, Timeout: 500 * time.Millisecond})
	for _, name := range []string{"hang", "crash"} {
		if err := convert(t, p, name); err == nil {
			t.Fatalf("%s: an error was expected", name)
		}
		if err := convert(t, p, "after-"+name); err != nil {
			t.Fatalf("the replacement worker must convert: %v", err)
		}
	}
}

// The native worker gets the values the Typst source reads.
func TestCompileSendsInputs(t *testing.T) {
	p := start(t, Options{Workers: 1, QueueSize: 1, Timeout: 5 * time.Second})
	dir := t.TempDir()
	in, out := filepath.Join(dir, "main.typ"), filepath.Join(dir, "out.pdf")
	if err := os.WriteFile(in, []byte("copy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Compile(context.Background(), in, out, map[string]string{"data": `{"nom":"x"}`}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != `COPY {"NOM":"X"}` {
		t.Fatalf("output %q %v", got, err)
	}
}

func TestRecycle(t *testing.T) {
	p := start(t, Options{Workers: 1, QueueSize: 5, Timeout: 5 * time.Second, RecycleAfter: 2})
	for i := range 5 {
		if err := convert(t, p, "doc"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
}

// Recycle replaces every worker, so new fonts are seen.
func TestRecycleAll(t *testing.T) {
	p := start(t, Options{Workers: 2, QueueSize: 5, Timeout: 5 * time.Second})
	pids := func() map[int]bool {
		p.mu.Lock()
		defer p.mu.Unlock()
		out := map[int]bool{}
		for w := range p.workers {
			out[w.cmd.Process.Pid] = true
		}
		return out
	}
	old := pids()
	p.Recycle()
	for i := range 3 {
		if err := convert(t, p, "doc"+string(rune('a'+i))); err != nil {
			t.Fatal(err)
		}
	}
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		now := pids()
		fresh := len(now) == 2
		for pid := range now {
			fresh = fresh && !old[pid]
		}
		if fresh {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("workers not replaced: before %v, now %v", old, now)
		}
	}
}

func TestBusy(t *testing.T) {
	p := start(t, Options{Workers: 1, QueueSize: 0, Timeout: 2 * time.Second})
	done := make(chan error)
	go func() { done <- convert(t, p, "hang") }()
	time.Sleep(200 * time.Millisecond)
	if _, err := p.Convert(context.Background(), "/nope", "/nope.pdf", "pdf", ""); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", err)
	}
	<-done
}
