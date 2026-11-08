package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"docibis/internal/store"
)

// A space gathers the templates of a team by the start of their paths. An
// API key kept to a space only finds its templates, and produces documents
// within its own quotas and those of the space.

const maxQuota = 100_000_000

// spacePrefixes reads the prefixes of a space, each ending with a slash.
func spacePrefixes(raw string) []string {
	out := []string{}
	for _, line := range strings.Split(raw, "\n") {
		p := strings.Trim(strings.TrimSpace(strings.ReplaceAll(line, `\`, "/")), "/")
		if p != "" && !slices.Contains(out, p+"/") {
			out = append(out, p+"/")
		}
	}
	return out
}

// inSpace tells whether a template belongs to a space; no prefix is the
// whole library.
func inSpace(p string, prefixes []string) bool {
	return len(prefixes) == 0 || slices.ContainsFunc(prefixes, func(pre string) bool { return strings.HasPrefix(p, pre) })
}

func keyScope(id int64) string   { return "key:" + strconv.FormatInt(id, 10) }
func spaceScope(id int64) string { return "space:" + strconv.FormatInt(id, 10) }

// keySpace returns the space the key of the request is kept to; nil when it
// may use the whole library.
func (s *Server) keySpace(ctx context.Context) (*store.Space, error) {
	k, ok := keyFrom(ctx)
	if !ok || k.SpaceID == 0 {
		return nil, nil
	}
	sp, found, err := s.db.Load().SpaceByID(ctx, k.SpaceID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &tryError{http.StatusNotFound, "L'espace de cette clé n'existe plus : demandez une autre clé."}
	}
	return &sp, nil
}

// keyPrefixes returns the prefixes of the space of the key of the request;
// none when it may use the whole library.
func (s *Server) keyPrefixes(ctx context.Context) ([]string, error) {
	sp, err := s.keySpace(ctx)
	if err != nil || sp == nil {
		return nil, err
	}
	return spacePrefixes(sp.Prefixes), nil
}

// keyTemplates returns the library ids of the templates the key of the
// request may use; nil when it may use the whole library.
func (s *Server) keyTemplates(ctx context.Context) ([]int64, error) {
	prefixes, err := s.keyPrefixes(ctx)
	if err != nil || len(prefixes) == 0 {
		return nil, err
	}
	rows, err := s.db.Load().Templates(ctx, "")
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for _, t := range rows {
		if inSpace(t.Path, prefixes) {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

// checkSpace refuses a template outside the space of the key of the request.
func (s *Server) checkSpace(ctx context.Context, p string) error {
	sp, err := s.keySpace(ctx)
	if err != nil || sp == nil || inSpace(p, spacePrefixes(sp.Prefixes)) {
		return err
	}
	return &tryError{http.StatusNotFound, fmt.Sprintf("Le modèle %q n'est pas dans l'espace « %s » de cette clé.", p, sp.Name)}
}

// producesDocuments tells whether a call of the API makes documents, and so
// counts in the quotas.
func producesDocuments(method, p string) bool {
	return method == http.MethodPost && (p == "/v1/render" || p == "/v1/render/async" || p == "/v1/render/batch" ||
		strings.HasPrefix(p, "/v1/bundles/") && strings.HasSuffix(p, "/render"))
}

// quotaWait tells how many seconds a key waits before its quotas, or those of
// its space, allow documents again, and why; 0 when it may produce.
// shortcut: checked before, counted after: calls running at once may pass a full quota by their number.
func (s *Server) quotaWait(ctx context.Context, k store.APIKey) (int, string, error) {
	if k.ID <= 0 || k.UserID != 0 {
		return 0, "", nil
	}
	type limit struct {
		scope, who     string
		daily, monthly int
	}
	limits := []limit{{keyScope(k.ID), fmt.Sprintf("la clé « %s »", k.Name), k.DailyQuota, k.MonthlyQuota}}
	st := s.db.Load()
	if k.SpaceID != 0 {
		sp, ok, err := st.SpaceByID(ctx, k.SpaceID)
		if err != nil {
			return 0, "", err
		}
		if ok {
			limits = append(limits, limit{spaceScope(sp.ID), fmt.Sprintf("l'espace « %s »", sp.Name), sp.DailyQuota, sp.MonthlyQuota})
		}
	}
	var scopes []string
	for _, l := range limits {
		if l.daily > 0 || l.monthly > 0 {
			scopes = append(scopes, l.scope)
		}
	}
	if len(scopes) == 0 {
		return 0, "", nil
	}
	now := time.Now()
	day, month, err := st.Usage(ctx, now, scopes...)
	if err != nil {
		return 0, "", err
	}
	wait := func(until time.Time) int { return max(int(until.Sub(now).Seconds())+1, 1) }
	for _, l := range limits {
		if l.daily > 0 && day[l.scope] >= int64(l.daily) {
			return wait(time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())),
				fmt.Sprintf("Quota du jour atteint pour %s : %d documents. Il repart à minuit.", l.who, l.daily), nil
		}
		if l.monthly > 0 && month[l.scope] >= int64(l.monthly) {
			return wait(time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())),
				fmt.Sprintf("Quota du mois atteint pour %s : %d documents. Il repart le 1er du mois.", l.who, l.monthly), nil
		}
	}
	return 0, "", nil
}

// countDocument counts a document made with a key, for its quotas and those
// of its space.
func (s *Server) countDocument(ctx context.Context) {
	k, ok := keyFrom(ctx)
	if !ok || k.ID <= 0 || k.UserID != 0 {
		return
	}
	scopes := []string{keyScope(k.ID)}
	if k.SpaceID != 0 {
		scopes = append(scopes, spaceScope(k.SpaceID))
	}
	if err := s.db.Load().AddUsage(ctx, time.Now(), scopes...); err != nil {
		s.logger.Warn("document not counted in the quotas", "key", k.Prefix, "error", err)
	}
}

// spaceInput checks what an administrator gives for a space.
func (s *Server) spaceInput(ctx context.Context, in *SpaceInput, id int64) (store.Space, error) {
	if in == nil {
		return store.Space{}, refuse("Donnez un nom à l'espace.")
	}
	sp := store.Space{ID: id, Name: strings.TrimSpace(in.Name), DailyQuota: valueOf(in.DailyQuota), MonthlyQuota: valueOf(in.MonthlyQuota)}
	if sp.Name == "" || len([]rune(sp.Name)) > 60 {
		return sp, refuse("Donnez un nom à l'espace (60 caractères au plus), par exemple celui de l'équipe.")
	}
	if sp.DailyQuota < 0 || sp.DailyQuota > maxQuota || sp.MonthlyQuota < 0 || sp.MonthlyQuota > maxQuota {
		return sp, refuse("Quotas : de 0 (sans limite) à %d documents.", maxQuota)
	}
	var prefixes []string
	for _, p := range valueOf(in.Prefixes) {
		if p = strings.Trim(strings.TrimSpace(p), `/\`); p == "" {
			continue
		}
		clean, err := cleanPath(p)
		if err != nil {
			return sp, refuse("Début de chemin invalide : %q.", p)
		}
		prefixes = append(prefixes, clean)
	}
	if len(prefixes) > 50 {
		return sp, refuse("50 débuts de chemin au plus.")
	}
	sp.Prefixes = strings.Join(prefixes, "\n")
	spaces, err := s.db.Load().Spaces(ctx)
	if err != nil {
		return sp, err
	}
	if slices.ContainsFunc(spaces, func(o store.Space) bool { return o.ID != id && strings.EqualFold(o.Name, sp.Name) }) {
		return sp, refuse("Un espace s'appelle déjà %q.", sp.Name)
	}
	return sp, nil
}

// spacesOut describes the spaces with their templates, keys and usage.
func (s *Server) spacesOut(ctx context.Context, spaces []store.Space) ([]Space, error) {
	st := s.db.Load()
	templates, err := st.Templates(ctx, "")
	if err != nil {
		return nil, err
	}
	keys, err := st.APIKeys(ctx)
	if err != nil {
		return nil, err
	}
	scopes := make([]string, 0, len(spaces))
	for _, sp := range spaces {
		scopes = append(scopes, spaceScope(sp.ID))
	}
	day, month, err := st.Usage(ctx, time.Now(), scopes...)
	if err != nil {
		return nil, err
	}
	out := make([]Space, 0, len(spaces))
	for _, sp := range spaces {
		prefixes := spacePrefixes(sp.Prefixes)
		item := Space{Id: sp.ID, Name: sp.Name, Prefixes: prefixes, DailyQuota: sp.DailyQuota, MonthlyQuota: sp.MonthlyQuota,
			CreatedAt: sp.CreatedAt, CreatedBy: sp.CreatedBy, Usage: Usage{Today: day[spaceScope(sp.ID)], Month: month[spaceScope(sp.ID)]}}
		for _, t := range templates {
			if inSpace(t.Path, prefixes) {
				item.Templates++
			}
		}
		for _, k := range keys {
			if k.SpaceID == sp.ID && apiKey(k).Status == ApiKeyStatusActive {
				item.Keys++
			}
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Server) ListSpaces(ctx context.Context, _ ListSpacesRequestObject) (ListSpacesResponseObject, error) {
	spaces, err := s.db.Load().Spaces(ctx)
	if err != nil {
		return nil, err
	}
	out, err := s.spacesOut(ctx, spaces)
	if err != nil {
		return nil, err
	}
	return ListSpaces200JSONResponse(out), nil
}

// saveSpace saves a space and describes it.
func (s *Server) saveSpace(ctx context.Context, in *SpaceInput, id int64) (Space, error) {
	sp, err := s.spaceInput(ctx, in, id)
	if err != nil {
		return Space{}, err
	}
	u, _ := userFrom(ctx)
	if id == 0 {
		sp.CreatedBy = u.Username
	}
	if err := s.db.Load().SaveSpace(ctx, &sp); err != nil {
		return Space{}, err
	}
	saved, _, err := s.db.Load().SpaceByID(ctx, sp.ID)
	if err != nil {
		return Space{}, err
	}
	out, err := s.spacesOut(ctx, []store.Space{saved})
	if err != nil {
		return Space{}, err
	}
	action := map[bool]string{true: "space.create", false: "space.update"}[id == 0]
	s.audit(ctx, u.Username, action, "space:"+saved.Name, fmt.Sprintf("%s ; %d par jour, %d par mois", strings.Join(out[0].Prefixes, " "), saved.DailyQuota, saved.MonthlyQuota))
	return out[0], nil
}

func (s *Server) CreateSpace(ctx context.Context, req CreateSpaceRequestObject) (CreateSpaceResponseObject, error) {
	out, err := s.saveSpace(ctx, req.Body, 0)
	var te *tryError
	if errors.As(err, &te) {
		return CreateSpace400ApplicationProblemPlusJSONResponse{badRequest(te.msg)}, nil
	}
	if err != nil {
		return nil, err
	}
	return CreateSpace201JSONResponse(out), nil
}

func (s *Server) UpdateSpace(ctx context.Context, req UpdateSpaceRequestObject) (UpdateSpaceResponseObject, error) {
	if _, ok, err := s.db.Load().SpaceByID(ctx, req.Id); err != nil {
		return nil, err
	} else if !ok {
		return UpdateSpace404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Espace inconnu."))}, nil
	}
	out, err := s.saveSpace(ctx, req.Body, req.Id)
	var te *tryError
	if errors.As(err, &te) {
		return UpdateSpace400ApplicationProblemPlusJSONResponse{badRequest(te.msg)}, nil
	}
	if err != nil {
		return nil, err
	}
	return UpdateSpace200JSONResponse(out), nil
}

func (s *Server) DeleteSpace(ctx context.Context, req DeleteSpaceRequestObject) (DeleteSpaceResponseObject, error) {
	st := s.db.Load()
	sp, ok, err := st.SpaceByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteSpace404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Espace inconnu."))}, nil
	}
	if _, err := st.DeleteSpace(ctx, req.Id); errors.Is(err, store.ErrSpaceUsed) {
		return DeleteSpace400ApplicationProblemPlusJSONResponse{badRequest("Des clés actives utilisent cet espace : changez leur espace ou révoquez-les d'abord.")}, nil
	} else if err != nil {
		return nil, err
	}
	u, _ := userFrom(ctx)
	s.audit(ctx, u.Username, "space.delete", "space:"+sp.Name, "")
	return DeleteSpace204Response{}, nil
}
