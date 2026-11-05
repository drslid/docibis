package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"

	"docibis/internal/store"
)

// aliasesFile lists the former names of a template, one per line: the
// X.aliases.txt of a zip of templates.
func aliasesFile(ctx context.Context, st *store.Store, id int64) ([]byte, error) {
	rows, err := st.TemplateAliases(ctx, id)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	var b strings.Builder
	for _, a := range rows {
		b.WriteString(a.Alias + "\n")
	}
	return []byte(b.String()), nil
}

// RenameTemplate gives a template a new path; the old one becomes a former
// name, so that the applications still calling it find the template.
func (s *Server) RenameTemplate(ctx context.Context, req RenameTemplateRequestObject) (RenameTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return RenameTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	p, err := cleanPath(req.Body.Path)
	if err == nil && !strings.EqualFold(path.Ext(p), path.Ext(t.Path)) {
		err = refuse("Le nouveau chemin garde l'extension du modèle (%s) : %q.", path.Ext(t.Path), p)
	}
	if err != nil {
		_, pb := s.failure(err)
		return RenameTemplate400ApplicationProblemPlusJSONResponse{BadRequestApplicationProblemPlusJSONResponse(pb)}, nil
	}
	me, _ := userFrom(ctx)
	if err := st.RenameTemplate(ctx, t.ID, p, me.Username); errors.Is(err, store.ErrNameTaken) {
		return RenameTemplate409ApplicationProblemPlusJSONResponse{ConflictApplicationProblemPlusJSONResponse(
			problem(http.StatusConflict, fmt.Sprintf("%q est déjà le nom ou un ancien nom d'un autre modèle.", p)))}, nil
	} else if err != nil {
		return nil, err
	}
	if p != t.Path {
		s.audit(ctx, me.Username, "template.rename", "template:"+p, "ancien nom "+t.Path)
	}
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return RenameTemplate200JSONResponse(out), nil
}

// MergeTemplate moves another template into this one: the versions whose
// file it lacks, its names as former names; the other one is deleted.
func (s *Server) MergeTemplate(ctx context.Context, req MergeTemplateRequestObject) (MergeTemplateResponseObject, error) {
	st := s.db.Load()
	target, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return MergeTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	source, ok, err := st.TemplateByID(ctx, req.Body.From)
	if err != nil {
		return nil, err
	}
	switch {
	case !ok:
		return MergeTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle à fusionner inconnu."))}, nil
	case source.ID == target.ID:
		return MergeTemplate400ApplicationProblemPlusJSONResponse{badRequest("Un modèle ne se fusionne pas avec lui-même.")}, nil
	case source.Kind != target.Kind:
		return MergeTemplate400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("%q et %q ne sont pas du même type.", source.Path, target.Path))}, nil
	}
	me, _ := userFrom(ctx)
	added, err := st.MergeTemplate(ctx, source.ID, target.ID, me.Username)
	if err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "template.merge", "template:"+target.Path, fmt.Sprintf("%s fusionné, %d versions ajoutées", source.Path, added))
	out, _, err := s.detail(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	return MergeTemplate200JSONResponse(out), nil
}

// DeleteTemplateAlias removes a former name of a template.
func (s *Server) DeleteTemplateAlias(ctx context.Context, req DeleteTemplateAliasRequestObject) (DeleteTemplateAliasResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if ok {
		ok, err = st.DeleteTemplateAlias(ctx, t.ID, req.Params.Name)
		if err != nil {
			return nil, err
		}
	}
	if !ok {
		return DeleteTemplateAlias404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Ancien nom inconnu pour ce modèle."))}, nil
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "template.alias_delete", "template:"+t.Path, "ancien nom "+req.Params.Name)
	return DeleteTemplateAlias204Response{}, nil
}
