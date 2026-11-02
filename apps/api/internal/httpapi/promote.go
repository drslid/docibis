package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"docibis/internal/store"
)

// A version of a template goes from one DocIbis to the next (INT, then REC,
// then PROD) in one click: the console sends it, with its reference, to
// /v1/templates/import of the other environment, with a key of that
// environment allowed to receive templates. The environments and their keys
// are set by the administrators.

const environmentsKey = "promotion.environments"

type environment struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Key  string `json:"key"`
}

func (s *Server) environments(ctx context.Context) ([]environment, error) {
	raw, ok, err := s.db.Load().Setting(ctx, environmentsKey)
	if err != nil || !ok || raw == "" {
		return []environment{}, err
	}
	var envs []environment
	if err := json.Unmarshal([]byte(raw), &envs); err != nil {
		return []environment{}, nil
	}
	return envs, nil
}

// maskKey shows the start of a key, enough to recognize it.
func maskKey(key string) string {
	if key == "" {
		return ""
	}
	if prefix, _, ok := strings.Cut(strings.TrimPrefix(key, "dib_"), "_"); ok {
		return "dib_" + prefix + "_…"
	}
	return "…"
}

func environmentsOut(envs []environment) []Environment {
	out := make([]Environment, 0, len(envs))
	for _, e := range envs {
		item := Environment{Name: e.Name, Url: e.URL}
		if e.Key != "" {
			item.Key = ptr(maskKey(e.Key))
		}
		out = append(out, item)
	}
	return out
}

func (s *Server) ListEnvironments(ctx context.Context, _ ListEnvironmentsRequestObject) (ListEnvironmentsResponseObject, error) {
	envs, err := s.environments(ctx)
	if err != nil {
		return nil, err
	}
	return ListEnvironments200JSONResponse(environmentsOut(envs)), nil
}

// environmentURL checks the address of another DocIbis: HTTPS, or HTTP on
// this machine for tests.
func environmentURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", refuse("Adresse invalide : %q (par exemple https://docibis-rec.exemple.fr).", raw)
	}
	local := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return "", refuse("L'adresse de %q doit commencer par https://.", raw)
	}
	return strings.TrimSuffix(u.Scheme+"://"+u.Host+u.Path, "/"), nil
}

func (s *Server) SaveEnvironments(ctx context.Context, req SaveEnvironmentsRequestObject) (SaveEnvironmentsResponseObject, error) {
	if req.Body == nil {
		return SaveEnvironments400ApplicationProblemPlusJSONResponse{badRequest("Donnez la liste des environnements.")}, nil
	}
	if len(*req.Body) > 10 {
		return SaveEnvironments400ApplicationProblemPlusJSONResponse{badRequest("10 environnements au plus.")}, nil
	}
	before, err := s.environments(ctx)
	if err != nil {
		return nil, err
	}
	var envs []environment
	names := []string{}
	for _, in := range *req.Body {
		name := strings.TrimSpace(in.Name)
		if name == "" || len([]rune(name)) > 20 {
			return SaveEnvironments400ApplicationProblemPlusJSONResponse{badRequest("Nom d'environnement : de 1 à 20 caractères (REC, PROD…).")}, nil
		}
		if slices.ContainsFunc(names, func(n string) bool { return strings.EqualFold(n, name) }) {
			return SaveEnvironments400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("%q apparaît deux fois.", name))}, nil
		}
		address, err := environmentURL(in.Url)
		if err != nil {
			_, pb := s.failure(err)
			return SaveEnvironments400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
		}
		key := trimmed(in.Key)
		if strings.HasSuffix(key, "…") {
			// The masked key read before: keep the saved one.
			key = ""
			for _, old := range before {
				if strings.EqualFold(old.Name, name) && maskKey(old.Key) == trimmed(in.Key) {
					key = old.Key
				}
			}
		} else if key != "" && (!strings.HasPrefix(key, "dib_") || len(key) > 200) {
			return SaveEnvironments400ApplicationProblemPlusJSONResponse{badRequest("La clé est une clé API de l'autre DocIbis (dib_…), créée avec « recevoir des modèles ».")}, nil
		}
		envs = append(envs, environment{Name: name, URL: address, Key: key})
		names = append(names, name)
	}
	raw, _ := json.Marshal(envs)
	if err := s.db.Load().SetSetting(ctx, environmentsKey, string(raw)); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "environments.update", "", strings.Join(names, ", "))
	return SaveEnvironments200JSONResponse(environmentsOut(envs)), nil
}

func promotionOut(p store.Promotion) Promotion {
	return Promotion{Id: p.ID, CreatedAt: p.CreatedAt, Version: p.Version, Environment: p.Environment, Status: PromotionStatus(p.Status), Detail: p.Detail, Actor: p.Actor}
}

func (s *Server) ListPromotions(ctx context.Context, req ListPromotionsRequestObject) (ListPromotionsResponseObject, error) {
	rows, err := s.db.Load().Promotions(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	out := make(ListPromotions200JSONResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, promotionOut(p))
	}
	return out, nil
}

// versionZip packs a version as the import reads it: the template, and its
// reference with its data.
func versionZip(t store.Template, v store.TemplateVersion) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, content []byte) error {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: importPrefix + name, Method: zip.Deflate, Modified: time.Now()})
		if err == nil {
			_, err = w.Write(content)
		}
		return err
	}
	if err := add(t.Path, v.Content); err != nil {
		return nil, err
	}
	if len(v.Reference) > 0 {
		if err := add(t.Path+".reference.pdf", v.Reference); err != nil {
			return nil, err
		}
		if err := add(t.Path+".data.json", []byte(v.ReferenceData)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

var promotionClient = &http.Client{
	Timeout:       2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// sendVersion posts a version to another DocIbis and tells what it did.
func (s *Server) sendVersion(ctx context.Context, env environment, t store.Template, v store.TemplateVersion, publish bool) (string, error) {
	pack, err := versionZip(t, v)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "promotion.zip")
	if err == nil {
		_, err = fw.Write(pack)
	}
	if err == nil {
		err = mw.WriteField("publish", fmt.Sprint(publish))
	}
	if err == nil {
		err = mw.WriteField("from", s.cfg.Load().String("instance_name"))
	}
	if err == nil {
		err = mw.Close()
	}
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, env.URL+"/v1/templates/import", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+env.Key)
	resp, err := promotionClient.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("%s ne répond pas (%v)", env.Name, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var p Problem
		detail := http.StatusText(resp.StatusCode)
		if json.Unmarshal(raw, &p) == nil && p.Detail != nil {
			detail = *p.Detail
		}
		return "", fmt.Errorf("%s a refusé (%d) : %s", env.Name, resp.StatusCode, shorten(detail, 300))
	}
	var report ImportReport
	if err := json.Unmarshal(raw, &report); err != nil {
		return "", fmt.Errorf("%s a répondu sans compte rendu", env.Name)
	}
	if len(report.Errors) > 0 {
		return "", fmt.Errorf("%s : %s", env.Name, shorten(strings.Join(report.Errors, " ; "), 300))
	}
	var what []string
	switch {
	case report.Created > 0:
		what = append(what, "créé")
	case report.Updated > 0:
		what = append(what, "nouvelle version")
	default:
		what = append(what, "déjà à jour")
	}
	if report.References > 0 {
		what = append(what, "avec sa référence")
	}
	if publish {
		what = append(what, "publiée")
	}
	return strings.Join(what, ", "), nil
}

func (s *Server) PromoteTemplate(ctx context.Context, req PromoteTemplateRequestObject) (PromoteTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return PromoteTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if req.Body == nil || req.Body.Version < 1 || req.Body.Version > t.Versions {
		return PromoteTemplate400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("Version à envoyer : de 1 à %d.", t.Versions))}, nil
	}
	envs, err := s.environments(ctx)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(envs, func(e environment) bool { return strings.EqualFold(e.Name, strings.TrimSpace(req.Body.Environment)) })
	if i < 0 {
		return PromoteTemplate400ApplicationProblemPlusJSONResponse{badRequest("Environnement inconnu : ajoutez-le dans Configuration, Environnements.")}, nil
	}
	env := envs[i]
	if env.Key == "" {
		return PromoteTemplate400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("Ajoutez la clé de %s dans Configuration, Environnements.", env.Name))}, nil
	}
	v, ok, err := st.TemplateVersion(ctx, t.ID, req.Body.Version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return PromoteTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Version inconnue."))}, nil
	}
	publish := req.Body.Publish != nil && *req.Body.Publish
	me, _ := userFrom(ctx)
	p := store.Promotion{TemplateID: t.ID, Version: v.Number, Environment: env.Name, Status: "ok", Actor: me.Username}
	if p.Detail, err = s.sendVersion(ctx, env, t, v, publish); err != nil {
		p.Status, p.Detail = "error", err.Error()
		s.logger.Warn("promotion failed", "template", t.Path, "version", v.Number, "environment", env.Name, "error", err)
	}
	if err := st.RecordPromotion(ctx, &p); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "template.promote", "template:"+t.Path, fmt.Sprintf("version %d vers %s : %s", v.Number, env.Name, p.Detail))
	return PromoteTemplate200JSONResponse(promotionOut(p)), nil
}

// ReceiveTemplates takes the templates sent by another environment, with a
// key allowed to receive them.
func (s *Server) ReceiveTemplates(ctx context.Context, req ReceiveTemplatesRequestObject) (ReceiveTemplatesResponseObject, error) {
	k, _ := keyFrom(ctx)
	if !slices.Contains(strings.Split(k.Scopes, ","), string(Templates)) {
		return ReceiveTemplates403ApplicationProblemPlusJSONResponse{ForbiddenApplicationProblemPlusJSONResponse(problem(http.StatusForbidden,
			"Cette clé ne peut pas recevoir de modèles : créez une clé avec « recevoir des modèles »."))}, nil
	}
	f, err := readLibraryForm(req.Body, map[string]int{"file": maxImportMB})
	var entries map[string]*importEntry
	if err == nil {
		entries, err = readImport(f.files["file"])
	}
	if err != nil {
		status, pb := s.failure(err)
		if status == http.StatusRequestEntityTooLarge {
			return ReceiveTemplates413ApplicationProblemPlusJSONResponse{TooLargeApplicationProblemPlusJSONResponse(pb)}, nil
		}
		return ReceiveTemplates400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	actor := "clé " + k.Name
	report := s.importTemplates(ctx, entries, actor, yes(f.values["publish"], false))
	s.describeLater(context.WithoutCancel(ctx))
	details := fmt.Sprintf("%d créés, %d mis à jour, %d inchangés, %d références, %d erreurs",
		report.Created, report.Updated, report.Unchanged, report.References, len(report.Errors))
	if from := shorten(f.values["from"], 20); from != "" {
		details += ", depuis " + from
	}
	s.audit(ctx, actor, "template.receive", "", details)
	return ReceiveTemplates200JSONResponse(report), nil
}
