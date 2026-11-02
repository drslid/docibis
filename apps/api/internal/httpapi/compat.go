package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

// docmosisPath answers like the /api/render of Docmosis: an application moves
// to DocIbis by changing the address and the access key only. The templates
// come from the library, under the same names.
const docmosisPath = "/api/render"

func docmosisError(msg string) DocmosisError {
	return DocmosisError{Succeeded: false, ShortMsg: msg, LongMsg: &msg}
}

// docmosisOptions are the render options of Docmosis that DocIbis takes, with
// the name of the DocIbis option.
var docmosisOptions = map[string]string{
	"passwordProtect": "passwordProtect", "pdfRestrictPassword": "pdfRestrictPassword",
	"pdfRestrictPrinting": "pdfRestrictPrinting", "pdfRestrictEditing": "pdfRestrictEditing",
	"pdfRestrictCopy": "pdfRestrictCopy", "pdfRestrictAllowAccessibilty": "pdfRestrictAllowAccessibilty",
	"pdfSkipEmptyPages": "pdfSkipEmptyPages", "devMode": "devMode", "pdfTagged": "accessible",
	"pdfWatermark": "watermark", "pdfWatermarkColor": "watermarkColor",
	"pdfWatermarkFontSize": "watermarkSize", "pdfWatermarkRotation": "watermarkAngle",
}

func (s *Server) DocmosisRender(ctx context.Context, req DocmosisRenderRequestObject) (DocmosisRenderResponseObject, error) {
	start := time.Now()
	var in DocmosisRender
	switch {
	case req.JSONBody != nil:
		in = *req.JSONBody
	case req.FormdataBody != nil:
		b := req.FormdataBody
		in = DocmosisRender{AccessKey: b.AccessKey, OutputFormat: b.OutputFormat, OutputName: b.OutputName, TemplateName: b.TemplateName}
		if b.Data != nil {
			in.Data = *b.Data
		}
	default:
		return DocmosisRender400JSONResponse(docmosisError("Demande vide : templateName, outputName et data attendus.")), nil
	}
	secret, _ := ctx.Value(bearerKey).(string)
	if in.AccessKey != nil && strings.TrimSpace(*in.AccessKey) != "" {
		secret = strings.TrimSpace(*in.AccessKey)
	}
	if s.db.Load() == nil {
		return DocmosisRender503JSONResponse(docmosisError(starting)), nil
	}
	k, ok, err := s.activeKey(ctx, secret)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DocmosisRender401JSONResponse(docmosisError("accessKey absente, inconnue, expirée ou révoquée : utilisez une clé API DocIbis (dib_…).")), nil
	}
	if wait, msg := s.throttle(k); wait > 0 {
		return DocmosisRender429JSONResponse(docmosisError(msg)), nil
	}
	ctx = context.WithValue(ctx, keyKey, k)

	format := ""
	if in.OutputFormat != nil {
		format = strings.ToLower(strings.TrimSpace(*in.OutputFormat))
	}
	if format == "" {
		format = strings.TrimPrefix(strings.ToLower(path.Ext(in.OutputName)), ".")
	}
	if format == "" {
		format = "pdf"
	}
	var data []byte
	switch d := in.Data.(type) {
	case nil:
		data = []byte("{}")
	case string:
		data = []byte(d)
	default:
		data, _ = json.Marshal(d)
	}
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(in.OutputName), `\`, "/"))
	if name == "." || name == "/" {
		name = ""
	}
	f := tryForm{templateName: in.TemplateName, data: data, hasData: true, outputName: name, opts: s.defaultOptions()}
	for key, value := range in.AdditionalProperties {
		option, ok := docmosisOptions[key]
		if !ok {
			continue
		}
		if _, err := f.opts.set(option, fmt.Sprint(value)); err != nil {
			_, p := s.failure(err)
			return DocmosisRender400JSONResponse(docmosisError(trimmed(p.Detail))), nil
		}
	}
	var doc document
	if s.renderer.Load() == nil {
		err = notReady
	} else {
		doc, err = s.produceForm(ctx, f, format)
	}
	s.record(ctx, "api", k.Name, doc, err, time.Since(start))
	if err != nil {
		status, p := s.failure(err)
		e := docmosisError(trimmed(p.Detail))
		switch status {
		case http.StatusNotFound:
			return DocmosisRender404JSONResponse(e), nil
		case http.StatusServiceUnavailable:
			return DocmosisRender503JSONResponse(e), nil
		default:
			return DocmosisRender400JSONResponse(e), nil
		}
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": doc.fileName()})
	headers := DocmosisRender200ResponseHeaders{ContentDisposition: &disposition}
	body, size := bytes.NewReader(doc.content), int64(len(doc.content))
	if doc.format == "docx" {
		return DocmosisRender200ApplicationvndOpenxmlformatsOfficedocumentWordprocessingmlDocumentResponse{Body: body, ContentLength: size, Headers: headers}, nil
	}
	return DocmosisRender200ApplicationpdfResponse{Body: body, ContentLength: size, Headers: headers}, nil
}
