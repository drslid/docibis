package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *Server) RenderDocument(ctx context.Context, req RenderDocumentRequestObject) (RenderDocumentResponseObject, error) {
	start := time.Now()
	doc, err := s.produce(ctx, req.Body, "")
	k, _ := keyFrom(ctx)
	s.record(ctx, "api", k.Name, doc, err, time.Since(start))
	if err != nil {
		status, p := s.failure(err)
		switch status {
		case http.StatusRequestEntityTooLarge:
			return RenderDocument413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusServiceUnavailable:
			return RenderDocument503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		case http.StatusNotFound:
			return RenderDocument404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		default:
			return RenderDocument400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	total, engine := ms(time.Since(start)), "standard"
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": doc.fileName()})
	headers := RenderDocument200ResponseHeaders{ContentDisposition: &disposition, XRenderMs: &total, XRenderEngine: &engine}
	if len(doc.missing) > 0 {
		missing := missingHeader(doc.missing)
		headers.XMissingFields = &missing
	}
	body, size := bytes.NewReader(doc.content), int64(len(doc.content))
	if doc.format == "docx" {
		return RenderDocument200ApplicationvndOpenxmlformatsOfficedocumentWordprocessingmlDocumentResponse{Body: body, ContentLength: size, Headers: headers}, nil
	}
	return RenderDocument200ApplicationpdfResponse{Body: body, ContentLength: size, Headers: headers}, nil
}

// missingHeader lists the missing data for X-Missing-Fields: paths separated
// by commas, each one URL-encoded (a header only carries ASCII).
func missingHeader(paths []string) string {
	const most = 50
	out := make([]string, 0, min(len(paths), most))
	for i, p := range paths {
		if i == most {
			out = append(out, "...")
			break
		}
		out = append(out, url.QueryEscape(p))
	}
	return strings.Join(out, ",")
}

func (s *Server) GetContract(context.Context, GetContractRequestObject) (GetContractResponseObject, error) {
	spec, err := GetSwagger()
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return GetContract200JSONResponse(out), nil
}
