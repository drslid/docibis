package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const maxBatch = 50

// batchError is a refusal of one document of a batch, with its status.
func batchError(i int, err error) error {
	var te *tryError
	if errors.As(err, &te) {
		return &tryError{te.status, fmt.Sprintf("Document %d : %s", i+1, te.msg)}
	}
	return err
}

// RenderBatch produces several documents in one call (a contract, its
// general conditions, a fixed PDF of the library…), joined in one PDF or
// returned in a zip.
func (s *Server) RenderBatch(ctx context.Context, req RenderBatchRequestObject) (RenderBatchResponseObject, error) {
	start := time.Now()
	k, _ := keyFrom(ctx)
	parts, names, protection, err := s.batch(ctx, req.Body)
	if err == nil && len(parts) == 0 {
		err = refuse("Donnez au moins un document.")
	}
	single := req.Body == nil || req.Body.Merge == nil || *req.Body.Merge
	var out []byte
	if err == nil && single {
		if out, err = mergePDF(ctx, parts); err == nil {
			out, err = protection.protect(ctx, out)
		}
	} else if err == nil {
		for i := range parts {
			if err == nil && strings.EqualFold(path.Ext(names[i]), ".pdf") {
				parts[i], err = protection.protect(ctx, parts[i])
			}
		}
		if err == nil {
			out, err = zipDocuments(parts, names)
		}
	}
	if err != nil {
		status, p := s.failure(err)
		s.logger.Warn("batch refused", "request_id", requestID(ctx), "key", k.Prefix, "status", status, "error", trimmed(p.Detail))
		switch status {
		case http.StatusNotFound:
			return RenderBatch404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusRequestEntityTooLarge:
			return RenderBatch413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusServiceUnavailable:
			return RenderBatch503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		default:
			return RenderBatch400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	total := ms(time.Since(start))
	name := "documents.pdf"
	if !single {
		name = "documents.zip"
	}
	if req.Body.OutputName != nil && strings.TrimSpace(*req.Body.OutputName) != "" {
		name = path.Base(strings.ReplaceAll(strings.TrimSpace(*req.Body.OutputName), `\`, "/"))
	}
	s.logger.Info("batch", "request_id", requestID(ctx), "key", k.Prefix, "actor", k.Name, "documents", len(parts), "merge", single, "size", len(out), "total_ms", total)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	headers := RenderBatch200ResponseHeaders{ContentDisposition: &disposition, XRenderMs: &total}
	if single {
		return RenderBatch200ApplicationpdfResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
	}
	return RenderBatch200ApplicationzipResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
}

// batch produces the documents of a batch, in order, and their names.
func (s *Server) batch(ctx context.Context, b *BatchRequest) ([][]byte, []string, renderOptions, error) {
	if b == nil || len(b.Documents) == 0 {
		return nil, nil, renderOptions{}, nil
	}
	if len(b.Documents) > maxBatch {
		return nil, nil, renderOptions{}, refuse("%d documents au plus par demande.", maxBatch)
	}
	if s.renderer.Load() == nil {
		return nil, nil, renderOptions{}, notReady
	}
	opts := s.defaultOptions()
	if err := opts.apply(b.Options); err != nil {
		return nil, nil, renderOptions{}, err
	}
	if err := opts.resolve(); err != nil {
		return nil, nil, renderOptions{}, err
	}
	// The passwords go on what is delivered: the joined PDF, or each PDF of
	// the zip.
	protection := opts
	opts.password, opts.restrict = "", restrictions{}
	format := "pdf"
	if b.Options != nil && b.Options.Format != nil {
		format = string(*b.Options.Format)
	}
	if format != "pdf" && (b.Merge == nil || *b.Merge) {
		return nil, nil, renderOptions{}, refuse("Seuls des PDF se joignent en un document : merge false pour un zip de %s.", format)
	}
	k, _ := keyFrom(ctx)
	parts, names := make([][]byte, 0, len(b.Documents)), make([]string, 0, len(b.Documents))
	for i, item := range b.Documents {
		var content []byte
		var name string
		switch {
		case item.Pdf != nil && item.TemplateName == nil:
			t, v, err := s.findTemplate(ctx, *item.Pdf)
			if err != nil {
				return nil, nil, renderOptions{}, batchError(i, err)
			}
			if t.Kind != "pdf" {
				return nil, nil, renderOptions{}, batchError(i, refuse("%q est un modèle à remplir : donnez-le dans templateName.", t.Path))
			}
			content, name = v.Content, path.Base(t.Path)
		case item.TemplateName != nil && item.Pdf == nil:
			data := []byte("{}")
			if item.Data != nil {
				data, _ = json.Marshal(*item.Data)
			}
			started := time.Now()
			doc, err := s.produceForm(ctx, tryForm{templateName: *item.TemplateName, data: data, hasData: true, opts: opts}, format)
			s.record(ctx, "api", k.Name, doc, err, time.Since(started))
			if err != nil {
				return nil, nil, renderOptions{}, batchError(i, err)
			}
			content, name = doc.content, doc.fileName()
		default:
			return nil, nil, renderOptions{}, refuse("Document %d : donnez templateName (avec data) ou pdf.", i+1)
		}
		if item.OutputName != nil && strings.TrimSpace(*item.OutputName) != "" {
			name = path.Base(strings.ReplaceAll(strings.TrimSpace(*item.OutputName), `\`, "/"))
		}
		parts, names = append(parts, content), append(names, name)
	}
	return parts, names, protection, nil
}

// mergePDF joins PDF documents, in order, into one.
func mergePDF(ctx context.Context, parts [][]byte) ([]byte, error) {
	if len(parts) == 1 {
		return parts[0], nil
	}
	in := make([]io.ReadSeeker, len(parts))
	for i, p := range parts {
		in[i] = bytes.NewReader(p)
	}
	var out bytes.Buffer
	if err := pdfapi.MergeRaw(ctx, in, &out, false, model.NewStatelessConfiguration()); err != nil {
		return nil, refuse("Les PDF n'ont pas pu être joints : %v", err)
	}
	return out.Bytes(), nil
}

// zipDocuments puts the documents in a zip, under distinct names.
func zipDocuments(parts [][]byte, names []string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	used := map[string]int{}
	for i, p := range parts {
		name := names[i]
		if n := used[name]; n > 0 {
			ext := path.Ext(name)
			name = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), n+1, ext)
		}
		used[names[i]]++
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
