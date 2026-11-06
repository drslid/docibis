package httpapi

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"docibis/internal/store"
)

// Documents produced in the background (POST /v1/render/async): the request
// is read and checked at once, then waits in memory for one of the
// async_workers. The state of the job and its document stay in the base for
// async_retention_minutes, for the key that asked only. When asked, the
// document is also sent by PUT to uploadUrl (a presigned S3 address, say),
// then a POST to callbackUrl tells the end, signed with WEBHOOK_SECRET. These
// two addresses never leave the memory: a presigned address is a credential.

const (
	// maxAsyncQueue is the most async_queue_size allows.
	maxAsyncQueue = 10000
	// asyncQueuedBytes bounds the templates and data waiting in memory.
	asyncQueuedBytes = 256 << 20
	maxHookURL       = 4000
	// A task notes the jobs it holds every aliveEvery; the jobs of a task
	// silent for staleAfter are failed: it stopped.
	aliveEvery = 30 * time.Second
	staleAfter = 2 * time.Minute
)

// hookWaits are the pauses between the attempts of an upload or a callback.
var hookWaits = []time.Duration{2 * time.Second, 10 * time.Second}

const jobStopped = "Interrompu : le service s'est arrêté avant la fin. Relancez la demande."

type asyncJob struct {
	// ctx keeps the request ID and the key of the request.
	ctx              context.Context
	rec              *store.RenderJob
	form             tryForm
	callback, upload string
	size             int
}

// asyncQueue holds the jobs of this task, waiting or running.
type asyncQueue struct {
	jobs          chan *asyncJob
	mu            sync.Mutex
	held          map[string]bool
	queued, bytes int
}

func (q *asyncQueue) reserve(j *asyncJob, limit int) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.queued >= min(limit, maxAsyncQueue) || q.bytes+j.size > asyncQueuedBytes {
		return false
	}
	q.queued++
	q.bytes += j.size
	q.held[j.rec.ID] = true
	return true
}

// taken frees the place of a job in the queue; done forgets it.
func (q *asyncQueue) taken(j *asyncJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.queued--
	q.bytes -= j.size
}

func (q *asyncQueue) done(j *asyncJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.held, j.rec.ID)
}

func (q *asyncQueue) ids() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	ids := make([]string, 0, len(q.held))
	for id := range q.held {
		ids = append(ids, id)
	}
	return ids
}

// hookTransport reaches the addresses of the callbacks and uploads, never
// the loopback nor the link-local addresses (the metadata of the ECS task and
// of the host), checked at connection time. The private ranges stay open: an
// S3 endpoint of the VPC, or an internal application, lives there.
var hookTransport = &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 10 * time.Second, Control: refuseLocal}).DialContext,
	TLSHandshakeTimeout:   10 * time.Second,
	ResponseHeaderTimeout: time.Minute,
	MaxIdleConnsPerHost:   4,
	IdleConnTimeout:       time.Minute,
}

func refuseLocal(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsLoopback() && !loopbackAllowed || awsLocal.Contains(ip) {
		return fmt.Errorf("address %s refused", host)
	}
	return nil
}

// awsLocal holds the IPv6 addresses of the instance metadata and of the
// services of the host (fd00:ec2::254…).
var awsLocal = &net.IPNet{IP: net.ParseIP("fd00:ec2::"), Mask: net.CIDRMask(32, 128)}

func newHookClient() *http.Client {
	return &http.Client{
		Transport:     hookTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// webhookKey signs the callbacks: WEBHOOK_SECRET, 32 characters at least.
func webhookKey() []byte {
	if k := strings.TrimSpace(os.Getenv("WEBHOOK_SECRET")); len(k) >= 32 {
		return []byte(k)
	}
	return nil
}

// sign is the X-DocIbis-Signature of a callback: "sha256=" then the
// HMAC-SHA256, in hexadecimal, of the timestamp, a dot and the body.
func sign(key []byte, timestamp string, body []byte) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(timestamp + "."))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

// setHook reads an address of /v1/render/async.
func (f *tryForm) setHook(name, value string) error {
	if len(value) > maxHookURL {
		return refuse("%s : adresse trop longue (%d caractères au plus).", name, maxHookURL)
	}
	if name == "callbackUrl" {
		f.callbackURL = strings.TrimSpace(value)
	} else {
		f.uploadURL = strings.TrimSpace(value)
	}
	return nil
}

// checkHook accepts an address of the caller in HTTPS, on a domain of the
// setting async_url_allowlist.
func checkHook(address, field string, allowed []string) error {
	if address == "" {
		return nil
	}
	u, err := url.Parse(address)
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil:
		return refuse("%s doit être une adresse https://.", field)
	case len(allowed) == 0:
		return refuse("%s : aucun domaine n'est autorisé ; un administrateur les ajoute dans la configuration (Rendu asynchrone).", field)
	case !hostAllowed(u.Hostname(), allowed):
		return refuse("%s : le domaine %s n'est pas autorisé (configuration, Rendu asynchrone).", field, u.Hostname())
	}
	return nil
}

// hostOf names the server of an address in the logs, never its path nor its
// query: they may carry a signature.
func hostOf(address string) string {
	if u, err := url.Parse(address); err == nil {
		return u.Hostname()
	}
	return ""
}

func (s *Server) retention() time.Duration {
	return time.Duration(s.cfg.Load().Int("async_retention_minutes")) * time.Minute
}

func (s *Server) RenderDocumentAsync(ctx context.Context, req RenderDocumentAsyncRequestObject) (RenderDocumentAsyncResponseObject, error) {
	job, err := s.submit(ctx, req.Body)
	if err != nil {
		status, p := s.failure(err)
		switch status {
		case http.StatusRequestEntityTooLarge:
			return RenderDocumentAsync413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusServiceUnavailable:
			return RenderDocumentAsync503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		case http.StatusNotFound:
			return RenderDocumentAsync404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		default:
			return RenderDocumentAsync400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	location := "/v1/jobs/" + job.Id
	return RenderDocumentAsync202JSONResponse{Body: job, Headers: RenderDocumentAsync202ResponseHeaders{Location: &location}}, nil
}

// submit checks what can be checked at once (the addresses, the template,
// the format, the options and the data), then queues the job.
func (s *Server) submit(ctx context.Context, body *multipart.Reader) (RenderJob, error) {
	if s.renderer.Load() == nil {
		return RenderJob{}, notReady
	}
	f, err := s.readForm(body)
	if err != nil {
		return RenderJob{}, err
	}
	cfg := s.cfg.Load()
	allowed := allowedHosts(cfg.String("async_url_allowlist"))
	if err := checkHook(f.callbackURL, "callbackUrl", allowed); err != nil {
		return RenderJob{}, err
	}
	if err := checkHook(f.uploadURL, "uploadUrl", allowed); err != nil {
		return RenderJob{}, err
	}
	if err := s.resolve(ctx, &f); err != nil {
		return RenderJob{}, err
	}
	if _, err := checkTemplate(f.name, f.template); err != nil && !isPDF(f.template) && !isTypst(f.name) {
		return RenderJob{}, err
	}
	format, err := outputFormat(cmp.Or(f.format, "pdf"))
	if err != nil {
		return RenderJob{}, err
	}
	opts := f.opts
	if err := opts.resolve(); err != nil {
		return RenderJob{}, err
	}
	var root map[string]any
	if !f.hasData || json.Unmarshal(f.data, &root) != nil {
		return RenderJob{}, refuse("Les données doivent être un objet JSON valide, par exemple { \"nom\": \"Dupont\" }.")
	}
	k, _ := keyFrom(ctx)
	rec := &store.RenderJob{ID: "job_" + randomText(20), KeyID: k.ID, Actor: k.Name, Status: "queued",
		Template: cmp.Or(f.templateName, f.name), Format: format, ExpiresAt: time.Now().Add(s.retention())}
	if f.uploadURL != "" {
		rec.Upload = "pending"
	}
	if f.callbackURL != "" {
		rec.Callback = "pending"
	}
	job := &asyncJob{ctx: context.WithoutCancel(ctx), rec: rec, form: f, callback: f.callbackURL, upload: f.uploadURL,
		size: len(f.template) + len(f.prepared) + len(f.data)}
	if !s.async.reserve(job, cfg.Int("async_queue_size")) {
		return RenderJob{}, &tryError{http.StatusServiceUnavailable, "Trop de documents en attente : réessayez dans un instant."}
	}
	if err := s.db.Load().CreateRenderJob(ctx, rec); err != nil {
		s.async.taken(job)
		s.async.done(job)
		s.logger.Error("async job not recorded", "request_id", requestID(ctx), "error", err)
		return RenderJob{}, &tryError{http.StatusServiceUnavailable, "La demande n'a pas pu être enregistrée : réessayez dans un instant."}
	}
	// Described before a worker takes it.
	out := s.jobOut(*rec)
	s.async.jobs <- job
	s.logger.Info("async job queued", "request_id", requestID(ctx), "job", out.Id, "key", k.Prefix, "template", out.Template,
		"upload", hostOf(f.uploadURL), "callback", hostOf(f.callbackURL))
	return out, nil
}

// jobOut describes a job; its document is readable once done, unless it went
// to uploadUrl.
func (s *Server) jobOut(j store.RenderJob) RenderJob {
	out := RenderJob{Id: j.ID, Status: RenderJobStatus(j.Status), CreatedAt: j.CreatedAt, FinishedAt: j.FinishedAt,
		ExpiresAt: j.ExpiresAt, Template: j.Template, Format: RenderJobFormat(j.Format)}
	if j.Error != "" {
		out.Error = &j.Error
	}
	if j.Status == "done" {
		out.Size = &j.Size
		if j.Upload != "done" {
			address := strings.TrimSuffix(s.cfg.Load().String("public_url"), "/") + "/v1/jobs/" + j.ID + "/document"
			out.DocumentUrl = &address
		}
	}
	if j.Upload != "" {
		out.Upload = ptr(RenderJobUpload(j.Upload))
	}
	if j.Callback != "" {
		out.Callback = ptr(RenderJobCallback(j.Callback))
	}
	return out
}

const jobGone = "Travail inconnu, ou effacé après le délai de conservation (async_retention_minutes)."

func (s *Server) GetRenderJob(ctx context.Context, req GetRenderJobRequestObject) (GetRenderJobResponseObject, error) {
	k, _ := keyFrom(ctx)
	j, ok, err := s.db.Load().RenderJob(ctx, req.Id, k.ID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return GetRenderJob404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, jobGone))}, nil
	}
	return GetRenderJob200JSONResponse(s.jobOut(j)), nil
}

func (s *Server) GetRenderJobDocument(ctx context.Context, req GetRenderJobDocumentRequestObject) (GetRenderJobDocumentResponseObject, error) {
	k, _ := keyFrom(ctx)
	j, ok, err := s.db.Load().RenderJobDocument(ctx, req.Id, k.ID)
	if err != nil {
		return nil, err
	}
	conflict := func(detail string) GetRenderJobDocumentResponseObject {
		return GetRenderJobDocument409ApplicationProblemPlusJSONResponse{ConflictApplicationProblemPlusJSONResponse(problem(http.StatusConflict, detail))}
	}
	switch {
	case !ok:
		return GetRenderJobDocument404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, jobGone))}, nil
	case j.Status == "failed":
		return conflict("Le document n'a pas pu être produit : " + j.Error), nil
	case j.Status != "done":
		return conflict("Le document n'est pas encore prêt : réessayez dans un instant."), nil
	case len(j.Document) == 0:
		return conflict("Le document a été déposé à l'adresse uploadUrl : DocIbis ne l'a pas gardé."), nil
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": j.FileName})
	return documentResponse{body: j.Document, format: j.Format, headers: map[string]string{"Content-Disposition": disposition}}, nil
}

// StartAsync starts the workers of the documents produced in the
// background, once the LibreOffice workers are warm.
func (s *Server) StartAsync(ctx context.Context, workers int) {
	for range max(workers, 1) {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-s.async.jobs:
					s.async.taken(job)
					s.runJob(ctx, job)
					s.async.done(job)
				}
			}
		}()
	}
	go s.watchJobs(ctx)
}

// StopAsync ends the jobs still waiting when the service stops: they fail,
// and their callers are told.
func (s *Server) StopAsync() {
	var wg sync.WaitGroup
	for {
		select {
		case job := <-s.async.jobs:
			s.async.taken(job)
			wg.Go(func() {
				defer s.async.done(job)
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				rec, now := job.rec, time.Now()
				rec.Status, rec.Error, rec.FinishedAt, rec.ExpiresAt = "failed", jobStopped, &now, now.Add(s.retention())
				if rec.Upload != "" {
					rec.Upload = "failed"
				}
				s.finish(ctx, job)
			})
		default:
			wg.Wait()
			return
		}
	}
}

// watchJobs notes the jobs this task holds, fails those of a task that
// stopped, and forgets the jobs past their expiry with their documents.
func (s *Server) watchJobs(ctx context.Context) {
	t := time.NewTicker(aliveEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		st, retention := s.db.Load(), s.retention()
		if err := st.TouchRenderJobs(ctx, s.async.ids(), retention); err != nil {
			s.logger.Warn("async jobs not noted", "error", err)
		}
		if n, err := st.FailStaleRenderJobs(ctx, time.Now().Add(-staleAfter), jobStopped, retention); err != nil {
			s.logger.Warn("stopped async jobs not failed", "error", err)
		} else if n > 0 {
			s.logger.Warn("async jobs of a stopped task failed", "jobs", n)
		}
		if _, err := st.PurgeRenderJobs(ctx); err != nil {
			s.logger.Warn("async jobs purge failed", "error", err)
		}
	}
}

// runJob produces the document of a job, keeps it or uploads it, then calls
// back.
func (s *Server) runJob(stop context.Context, job *asyncJob) {
	st, rec, ctx := s.db.Load(), job.rec, job.ctx
	rec.Status = "running"
	if err := st.UpdateRenderJob(ctx, rec, "status"); err != nil {
		s.logger.Warn("async job not updated", "request_id", requestID(ctx), "job", rec.ID, "error", err)
	}
	start := time.Now()
	doc, err := s.produceForm(ctx, job.form, "")
	s.record(ctx, "api", rec.Actor, doc, err, time.Since(start))
	now := time.Now()
	rec.FinishedAt, rec.ExpiresAt = &now, now.Add(s.retention())
	if err != nil {
		_, p := s.failure(err)
		rec.Status, rec.Error = "failed", trimmed(p.Detail)
		if rec.Upload != "" {
			rec.Upload = "failed"
		}
	} else {
		rec.Status, rec.Format, rec.FileName, rec.Size, rec.Document = "done", doc.format, doc.fileName(), int64(len(doc.content)), doc.content
		if job.upload != "" {
			if err := s.upload(stop, job.upload, doc); err != nil {
				s.logger.Warn("async job: upload failed, the document stays readable", "request_id", requestID(ctx), "job", rec.ID,
					"host", hostOf(job.upload), "error", err)
				rec.Upload = "failed"
			} else {
				rec.Upload, rec.Document = "done", nil
			}
		}
	}
	s.finish(stop, job)
	s.logger.Info("async job finished", "request_id", requestID(ctx), "job", rec.ID, "status", rec.Status,
		"upload", rec.Upload, "callback", rec.Callback, "total_ms", ms(time.Since(start)))
}

// finish records the end of a job, its document included, then calls back.
func (s *Server) finish(stop context.Context, job *asyncJob) {
	st, rec, ctx := s.db.Load(), job.rec, job.ctx
	if err := st.UpdateRenderJob(ctx, rec); err != nil {
		s.logger.Error("async job not updated", "request_id", requestID(ctx), "job", rec.ID, "error", err)
	}
	rec.Document = nil
	if job.callback == "" {
		return
	}
	err := s.callBack(stop, job.callback, rec)
	rec.Callback = "done"
	if err != nil {
		s.logger.Warn("async job: callback failed", "request_id", requestID(ctx), "job", rec.ID, "host", hostOf(job.callback), "error", err)
		rec.Callback = "failed"
	}
	if err := st.UpdateRenderJob(ctx, rec, "callback"); err != nil {
		s.logger.Warn("async job not updated", "request_id", requestID(ctx), "job", rec.ID, "error", err)
	}
}

// upload sends the document by PUT to uploadUrl.
func (s *Server) upload(stop context.Context, address string, doc document) error {
	kind := contentType(doc.format)
	return s.hook(stop, 2*time.Minute, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, address, bytes.NewReader(doc.content))
		if err == nil {
			req.Header.Set("Content-Type", kind)
		}
		return req, err
	})
}

// callBack posts the state of the job to callbackUrl, as GET /v1/jobs/{id}
// describes it, signed when WEBHOOK_SECRET is set.
func (s *Server) callBack(stop context.Context, address string, rec *store.RenderJob) error {
	body, err := json.Marshal(s.jobOut(*rec))
	if err != nil {
		return err
	}
	return s.hook(stop, 15*time.Second, func(ctx context.Context) (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		ts := strconv.FormatInt(time.Now().Unix(), 10)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-DocIbis-Event", "render."+rec.Status)
		req.Header.Set("X-DocIbis-Job", rec.ID)
		req.Header.Set("X-DocIbis-Timestamp", ts)
		if s.hookKey != nil {
			req.Header.Set("X-DocIbis-Signature", sign(s.hookKey, ts, body))
		}
		return req, nil
	})
}

// hookStatus is an answer of the caller's server other than 2xx.
type hookStatus int

func (h hookStatus) Error() string { return "status " + strconv.Itoa(int(h)) }

// hook makes a request to an address of the caller, three times at most when
// the server fails or is busy. No redirection is followed.
func (s *Server) hook(stop context.Context, timeout time.Duration, build func(context.Context) (*http.Request, error)) error {
	for attempt := 0; ; attempt++ {
		err := s.hookOnce(stop, timeout, build)
		var status hookStatus
		retry := !errors.As(err, &status) || status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
		if err == nil || !retry || attempt == len(hookWaits) {
			return err
		}
		select {
		case <-stop.Done():
			return err
		case <-time.After(hookWaits[attempt]):
		}
	}
}

func (s *Server) hookOnce(stop context.Context, timeout time.Duration, build func(context.Context) (*http.Request, error)) error {
	ctx, cancel := context.WithTimeout(stop, timeout)
	defer cancel()
	req, err := build(ctx)
	if err != nil {
		return errors.New("invalid address")
	}
	req.Header.Set("User-Agent", "DocIbis")
	resp, err := s.hooks.Do(req)
	if err != nil {
		// Without the address: it may carry a signature.
		var ue *url.Error
		if errors.As(err, &ue) {
			return ue.Err
		}
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return hookStatus(resp.StatusCode)
	}
	return nil
}
