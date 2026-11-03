// Package render runs the LibreOffice workers: a fixed number of processes
// kept warm, a bounded queue, a time limit per conversion, and a restart
// when a worker hangs, crashes or has served enough conversions.
package render

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ErrBusy means every worker is busy and the queue is full: answer 503.
var ErrBusy = errors.New("all workers are busy")

// errUnreachable means the worker was gone before it read the request.
var errUnreachable = errors.New("worker unreachable")

// Options configures the pool.
type Options struct {
	// Command is the worker program and its arguments (worker.py run by the
	// Python of LibreOffice).
	Command []string
	// Env is added to the environment of the workers.
	Env          []string
	Workers      int
	QueueSize    int
	Timeout      time.Duration
	RecycleAfter int
	// StartTimeout bounds the start of a worker (LibreOffice loading).
	StartTimeout time.Duration
	Logger       *slog.Logger
}

// Pool hands conversions to warm workers.
type Pool struct {
	opts    Options
	idle    chan *worker
	slots   chan struct{}
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	workers map[*worker]bool
}

// Start launches the workers and returns once they are all ready.
func Start(ctx context.Context, opts Options) (*Pool, error) {
	if opts.Workers < 1 {
		opts.Workers = 1
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 45 * time.Second
	}
	if opts.StartTimeout <= 0 {
		opts.StartTimeout = time.Minute
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	pctx, cancel := context.WithCancel(context.Background())
	p := &Pool{
		opts:    opts,
		idle:    make(chan *worker, opts.Workers),
		slots:   make(chan struct{}, opts.Workers+opts.QueueSize),
		ctx:     pctx,
		cancel:  cancel,
		workers: map[*worker]bool{},
	}
	errs := make(chan error, opts.Workers)
	for range opts.Workers {
		go func() {
			w, err := p.spawn(ctx)
			if err == nil {
				p.idle <- w
			}
			errs <- err
		}()
	}
	var first error
	for range opts.Workers {
		if err := <-errs; err != nil && first == nil {
			first = err
		}
	}
	if first != nil {
		p.Close()
		return nil, first
	}
	return p, nil
}

// Convert converts in to out with a warm worker. It returns the time spent
// by LibreOffice.
func (p *Pool) Convert(ctx context.Context, in, out, format, filter string) (time.Duration, error) {
	return p.do(ctx, Request{In: in, Out: out, Format: format, Filter: filter})
}

// Compile makes the PDF out of the Typst source in, with the values it
// reads, on a worker of the native engine. It returns the time spent by
// Typst.
func (p *Pool) Compile(ctx context.Context, in, out string, inputs map[string]string) (time.Duration, error) {
	return p.do(ctx, Request{In: in, Out: out, Format: "pdf", Inputs: inputs})
}

func (p *Pool) do(ctx context.Context, req Request) (time.Duration, error) {
	select {
	case p.slots <- struct{}{}:
	default:
		return 0, ErrBusy
	}
	defer func() { <-p.slots }()

	for attempt := 0; ; attempt++ {
		w, err := p.take(ctx)
		if err != nil {
			return 0, err
		}
		d, err := p.convert(w, req)
		if errors.Is(err, errUnreachable) && attempt == 0 {
			// It died before reading the document: another one does it.
			continue
		}
		return d, err
	}
}

// take waits for an idle worker; one that died meanwhile is replaced.
func (p *Pool) take(ctx context.Context) (*worker, error) {
	for {
		var w *worker
		select {
		case w = <-p.idle:
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-p.ctx.Done():
			return nil, errors.New("pool closed")
		}
		select {
		case <-w.exited:
			p.opts.Logger.Warn("worker replaced", "reason", "stopped while idle", "conversions", w.next)
			p.retire(w, true)
			p.replace()
		default:
			return w, nil
		}
	}
}

func (p *Pool) convert(w *worker, req Request) (time.Duration, error) {
	w.next++
	req.ID = w.next
	reply, err := w.call(req, p.opts.Timeout)
	switch {
	case err != nil:
		// Hung or dead: replace it.
		p.opts.Logger.Warn("worker replaced", "reason", err.Error(), "conversions", w.next)
		p.retire(w, true)
		p.replace()
		return 0, err
	case p.opts.RecycleAfter > 0 && w.next >= p.opts.RecycleAfter:
		p.retire(w, false)
		p.replace()
	case reply.Error != "" && w.stopsAfterError():
		p.opts.Logger.Warn("worker replaced", "reason", "LibreOffice unusable after: "+reply.Error, "conversions", w.next)
		p.retire(w, true)
		p.replace()
	default:
		p.idle <- w
	}
	if reply.Error != "" {
		return time.Duration(reply.Ms) * time.Millisecond, errors.New(reply.Error)
	}
	return time.Duration(reply.Ms) * time.Millisecond, nil
}

// Stats is the load of the pool at a given moment.
type Stats struct {
	Workers, Busy, Queued int
}

// Stats tells how many workers convert and how many requests wait.
func (p *Pool) Stats() Stats {
	busy := max(p.opts.Workers-len(p.idle), 0)
	return Stats{Workers: p.opts.Workers, Busy: busy, Queued: max(len(p.slots)-busy, 0)}
}

// replace starts a worker in the background, retrying while it fails.
func (p *Pool) replace() {
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for delay := time.Second; ; delay = min(delay*2, 30*time.Second) {
			w, err := p.spawn(p.ctx)
			if err == nil {
				p.idle <- w
				return
			}
			p.opts.Logger.Error("worker start failed", "error", err)
			select {
			case <-p.ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

// Close stops every worker.
func (p *Pool) Close() {
	p.cancel()
	p.wg.Wait()
	p.mu.Lock()
	all := make([]*worker, 0, len(p.workers))
	for w := range p.workers {
		all = append(all, w)
	}
	p.mu.Unlock()
	for _, w := range all {
		p.retire(w, false)
	}
}

func (p *Pool) retire(w *worker, kill bool) {
	p.mu.Lock()
	delete(p.workers, w)
	p.mu.Unlock()
	w.stop(kill)
}

// worker is one worker process.
type worker struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	replies chan Reply
	exited  chan struct{}
	next    int
}

func (p *Pool) spawn(ctx context.Context) (*worker, error) {
	cmd := exec.Command(p.opts.Command[0], p.opts.Command[1:]...)
	cmd.Env = append(cmd.Environ(), p.opts.Env...)
	// The worker leads a process group with its LibreOffice: stopping the
	// group stops them all.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker: %w", err)
	}
	w := &worker{cmd: cmd, stdin: stdin, replies: make(chan Reply, 1), exited: make(chan struct{})}
	go func() {
		lines := bufio.NewScanner(stderr)
		for lines.Scan() {
			p.opts.Logger.Debug("libreoffice", "pid", cmd.Process.Pid, "line", lines.Text())
		}
	}()
	go func() {
		lines := bufio.NewScanner(stdout)
		for lines.Scan() {
			var r Reply
			if json.Unmarshal(lines.Bytes(), &r) == nil {
				w.replies <- r
			}
		}
		_ = cmd.Wait()
		// LibreOffice may outlive a worker that crashed.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		p.opts.Logger.Debug("worker exited", "pid", cmd.Process.Pid, "state", cmd.ProcessState.String())
		close(w.exited)
	}()
	ready := time.NewTimer(p.opts.StartTimeout)
	defer ready.Stop()
	select {
	case r := <-w.replies:
		if !r.Ready {
			w.stop(true)
			return nil, fmt.Errorf("worker did not start: %s", r.Error)
		}
		p.opts.Logger.Info("worker ready", "pid", cmd.Process.Pid, "start_ms", r.Ms)
	case <-w.exited:
		return nil, errors.New("worker exited while starting")
	case <-ready.C:
		w.stop(true)
		return nil, errors.New("worker start timed out")
	case <-ctx.Done():
		w.stop(true)
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.workers[w] = true
	p.mu.Unlock()
	return w, nil
}

func (w *worker) call(req Request, timeout time.Duration) (Reply, error) {
	line, _ := json.Marshal(req)
	if _, err := w.stdin.Write(append(line, '\n')); err != nil {
		return Reply{}, fmt.Errorf("%w: %w", errUnreachable, err)
	}
	limit := time.NewTimer(timeout)
	defer limit.Stop()
	for {
		select {
		case r := <-w.replies:
			if r.ID == req.ID {
				return r, nil
			}
		case <-w.exited:
			return Reply{}, errors.New("worker crashed")
		case <-limit.C:
			return Reply{}, fmt.Errorf("conversion took more than %s", timeout)
		}
	}
}

// stopsAfterError tells whether the worker exits after a failed document:
// it does so at once when LibreOffice no longer answers.
func (w *worker) stopsAfterError() bool {
	select {
	case <-w.exited:
		return true
	case <-time.After(200 * time.Millisecond):
		return false
	}
}

// stop ends the process: closing stdin asks it to exit; kill does not wait.
func (w *worker) stop(kill bool) {
	_ = w.stdin.Close()
	if !kill {
		select {
		case <-w.exited:
			return
		case <-time.After(5 * time.Second):
		}
	}
	_ = syscall.Kill(-w.cmd.Process.Pid, syscall.SIGKILL)
	<-w.exited
}
