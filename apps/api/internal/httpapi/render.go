package httpapi

import (
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"strconv"
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
	headers := map[string]string{
		"Content-Disposition": mime.FormatMediaType("attachment", map[string]string{"filename": doc.fileName()}),
		"X-Render-Ms":         strconv.Itoa(total),
		"X-Render-Engine":     engine,
	}
	if len(doc.missing) > 0 {
		headers["X-Missing-Fields"] = missingHeader(doc.missing)
	}
	return documentResponse{body: doc.content, format: doc.format, headers: headers}, nil
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
