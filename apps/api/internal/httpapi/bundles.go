package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strings"
	"time"

	"docibis/internal/merge"
	"docibis/internal/store"
)

// A bundle (liasse) is a set of documents produced together in one call:
// templates and fixed PDFs of the library, in order, each one under an
// optional condition on the data. The business changes what a bundle holds
// in the console; the applications only send its name and the data.

const maxBundleItems = 30

var bundleName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)

func bundleOut(b store.Bundle) Bundle {
	out := Bundle{
		Id: b.ID, Name: b.Name, Description: b.Description, Output: BundleOutput(b.Output), Items: []BundleItem{},
		CreatedAt: b.CreatedAt, CreatedBy: b.CreatedBy, UpdatedAt: b.UpdatedAt, UpdatedBy: b.UpdatedBy,
	}
	_ = json.Unmarshal([]byte(b.Items), &out.Items)
	return out
}

// bundleItems reads the items of a bundle as saved.
func bundleItems(b store.Bundle) []BundleItem {
	var items []BundleItem
	_ = json.Unmarshal([]byte(b.Items), &items)
	return items
}

func (s *Server) ListBundles(ctx context.Context, _ ListBundlesRequestObject) (ListBundlesResponseObject, error) {
	rows, err := s.db.Load().Bundles(ctx)
	if err != nil {
		return nil, err
	}
	out := make(ListBundles200JSONResponse, 0, len(rows))
	for _, b := range rows {
		out = append(out, bundleOut(b))
	}
	return out, nil
}

// checkBundle validates a bundle and fills b with it.
func (s *Server) checkBundle(ctx context.Context, in *BundleInput, b *store.Bundle) error {
	if in == nil {
		return refuse("Donnez le nom et les documents de la liasse.")
	}
	name := strings.TrimSpace(in.Name)
	if !bundleName.MatchString(name) {
		return refuse("Nom de liasse : lettres, chiffres, point, tiret et souligné, 100 caractères au plus (par exemple souscription-habitation).")
	}
	st := s.db.Load()
	if other, exists, err := st.BundleByName(ctx, name); err != nil {
		return err
	} else if exists && other.ID != b.ID {
		return refuse("Une liasse s'appelle déjà %q.", name)
	}
	if len(in.Items) == 0 || len(in.Items) > maxBundleItems {
		return refuse("Une liasse a de 1 à %d documents.", maxBundleItems)
	}
	items := make([]BundleItem, 0, len(in.Items))
	for i, item := range in.Items {
		p, err := cleanPath(item.Template)
		if err != nil {
			return refuse("Document %d : chemin invalide.", i+1)
		}
		if _, ok, err := st.TemplateByPath(ctx, p); err != nil {
			return err
		} else if !ok {
			return refuse("Document %d : %q n'est pas dans la bibliothèque.", i+1, p)
		}
		out := BundleItem{Template: p}
		if c := trimmed(item.Condition); c != "" {
			if len(c) > 500 {
				return refuse("Document %d : condition de 500 caractères au plus.", i+1)
			}
			if err := merge.CheckCondition(c); err != nil {
				return refuse("Document %d : condition illisible (%v).", i+1, err)
			}
			out.Condition = &c
		}
		if d := trimmed(item.DataPath); d != "" {
			if len(d) > 200 || strings.ContainsAny(d, " []{}") {
				return refuse("Document %d : partie des données invalide (par exemple contrat ou contrat.assure).", i+1)
			}
			out.DataPath = &d
		}
		items = append(items, out)
	}
	description := trimmed(in.Description)
	if len([]rune(description)) > 500 {
		return refuse("Description : 500 caractères au plus.")
	}
	output := "pdf"
	if in.Output != nil {
		output = string(*in.Output)
	}
	if output != "pdf" && output != "zip" {
		return refuse("Sortie : pdf ou zip.")
	}
	raw, _ := json.Marshal(items)
	b.Name, b.Description, b.Items, b.Output = name, description, string(raw), output
	return nil
}

func (s *Server) CreateBundle(ctx context.Context, req CreateBundleRequestObject) (CreateBundleResponseObject, error) {
	var b store.Bundle
	if err := s.checkBundle(ctx, req.Body, &b); err != nil {
		_, pb := s.failure(err)
		return CreateBundle400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	b.UpdatedBy = me.Username
	if err := s.db.Load().SaveBundle(ctx, &b); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "bundle.create", "bundle:"+b.Name, fmt.Sprintf("%d documents", len(bundleItems(b))))
	return CreateBundle201JSONResponse(bundleOut(b)), nil
}

func (s *Server) UpdateBundle(ctx context.Context, req UpdateBundleRequestObject) (UpdateBundleResponseObject, error) {
	st := s.db.Load()
	b, ok, err := st.BundleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Liasse inconnue."))}, nil
	}
	before := b.Name
	if err := s.checkBundle(ctx, req.Body, &b); err != nil {
		_, pb := s.failure(err)
		return UpdateBundle400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	b.UpdatedBy = me.Username
	if err := st.SaveBundle(ctx, &b); err != nil {
		return nil, err
	}
	details := fmt.Sprintf("%d documents", len(bundleItems(b)))
	if before != b.Name {
		details += ", renommée (avant : " + before + ")"
	}
	s.audit(ctx, me.Username, "bundle.update", "bundle:"+b.Name, details)
	return UpdateBundle200JSONResponse(bundleOut(b)), nil
}

func (s *Server) DeleteBundle(ctx context.Context, req DeleteBundleRequestObject) (DeleteBundleResponseObject, error) {
	st := s.db.Load()
	b, ok, err := st.BundleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if ok {
		ok, err = st.DeleteBundle(ctx, b.ID)
	}
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Liasse inconnue."))}, nil
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "bundle.delete", "bundle:"+b.Name, "")
	return DeleteBundle204Response{}, nil
}

// bundleData is the part of the data a document of a bundle receives.
func bundleData(data map[string]any, dataPath string) (map[string]any, error) {
	if dataPath == "" {
		return data, nil
	}
	var v any = data
	for _, key := range strings.Split(dataPath, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, refuse("Les données n'ont pas d'objet %q.", dataPath)
		}
		if v, ok = m[key]; !ok {
			return nil, refuse("Les données n'ont pas d'objet %q.", dataPath)
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, refuse("%q n'est pas un objet dans les données.", dataPath)
	}
	return m, nil
}

// produceBundle produces the documents of a bundle whose condition holds,
// in order, joined in one PDF or put in a zip.
func (s *Server) produceBundle(ctx context.Context, b store.Bundle, data map[string]any, source, actor string) ([]byte, int, error) {
	if s.renderer.Load() == nil {
		return nil, 0, notReady
	}
	if data == nil {
		return nil, 0, refuse("Les données doivent être un objet JSON.")
	}
	var parts [][]byte
	var names []string
	for i, item := range bundleItems(b) {
		if c := trimmed(item.Condition); c != "" && !merge.Condition(c, data) {
			continue
		}
		t, v, err := s.findTemplate(ctx, item.Template)
		if err != nil {
			return nil, 0, batchError(i, err)
		}
		if t.Kind == "pdf" {
			parts, names = append(parts, v.Content), append(names, path.Base(t.Path))
			continue
		}
		part, err := bundleData(data, trimmed(item.DataPath))
		if err != nil {
			return nil, 0, batchError(i, err)
		}
		raw, _ := json.Marshal(part)
		started := time.Now()
		doc, err := s.produceForm(ctx, tryForm{templateName: t.Path, data: raw, hasData: true, opts: s.defaultOptions()}, "pdf")
		s.record(ctx, source, actor, doc, err, time.Since(started))
		if err != nil {
			return nil, 0, batchError(i, err)
		}
		parts, names = append(parts, doc.content), append(names, doc.fileName())
	}
	if len(parts) == 0 {
		return nil, 0, refuse("Aucun document à produire : aucune condition de la liasse %q n'est remplie par ces données.", b.Name)
	}
	if b.Output == "zip" {
		out, err := zipDocuments(parts, names)
		return out, len(parts), err
	}
	out, err := mergePDF(ctx, parts)
	return out, len(parts), err
}

func bundleFileName(b store.Bundle, outputName *string) string {
	if n := trimmed(outputName); n != "" {
		return path.Base(strings.ReplaceAll(n, `\`, "/"))
	}
	return b.Name + "." + b.Output
}

// RenderBundle produces a bundle for an application.
func (s *Server) RenderBundle(ctx context.Context, req RenderBundleRequestObject) (RenderBundleResponseObject, error) {
	start := time.Now()
	k, _ := keyFrom(ctx)
	b, ok, err := s.db.Load().BundleByName(ctx, req.Name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RenderBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, fmt.Sprintf("Liasse inconnue : %q.", req.Name)))}, nil
	}
	var data map[string]any
	if req.Body != nil {
		data = req.Body.Data
	}
	out, n, err := s.produceBundle(ctx, b, data, "api", k.Name)
	if err != nil {
		status, p := s.failure(err)
		s.logger.Warn("bundle refused", "request_id", requestID(ctx), "bundle", b.Name, "key", k.Prefix, "status", status, "error", trimmed(p.Detail))
		switch status {
		case http.StatusNotFound:
			return RenderBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusServiceUnavailable:
			return RenderBundle503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		default:
			return RenderBundle400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	total := ms(time.Since(start))
	s.logger.Info("bundle", "request_id", requestID(ctx), "bundle", b.Name, "key", k.Prefix, "actor", k.Name, "documents", n, "size", len(out), "total_ms", total)
	var outputName *string
	if req.Body != nil {
		outputName = req.Body.OutputName
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": bundleFileName(b, outputName)})
	headers := RenderBundle200ResponseHeaders{ContentDisposition: &disposition, XRenderMs: &total}
	if b.Output == "zip" {
		return RenderBundle200ApplicationzipResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
	}
	return RenderBundle200ApplicationpdfResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
}

// TryBundle produces a bundle from the console, with test data.
func (s *Server) TryBundle(ctx context.Context, req TryBundleRequestObject) (TryBundleResponseObject, error) {
	b, ok, err := s.db.Load().BundleByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return TryBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Liasse inconnue."))}, nil
	}
	var data map[string]any
	if req.Body != nil {
		data = req.Body.Data
	}
	me, _ := userFrom(ctx)
	out, _, err := s.produceBundle(ctx, b, data, "console", me.Username)
	if err != nil {
		switch status, p := s.failure(err); status {
		case http.StatusNotFound:
			return TryBundle404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(p)}, nil
		case http.StatusServiceUnavailable:
			return TryBundle503ApplicationProblemPlusJSONResponse{busy(p)}, nil
		default:
			return TryBundle400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(p)}, nil
		}
	}
	var outputName *string
	if req.Body != nil {
		outputName = req.Body.OutputName
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": bundleFileName(b, outputName)})
	headers := TryBundle200ResponseHeaders{ContentDisposition: &disposition}
	if b.Output == "zip" {
		return TryBundle200ApplicationzipResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
	}
	return TryBundle200ApplicationpdfResponse{Body: bytes.NewReader(out), ContentLength: int64(len(out)), Headers: headers}, nil
}
