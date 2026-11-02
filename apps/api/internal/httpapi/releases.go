package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"docibis/internal/store"
	"docibis/internal/version"
)

// The "Nouveautés" page: notes of each version, in Markdown,
// written by the administrators, and one note written by DocIbis itself for
// each new version deployed, from the commits it brings.

// changesFile lists the commits of the image, newest first (sha, date,
// subject, tab-separated), written by the CI.
var changesFile = "/usr/local/lib/docibis/changes.txt"

const releaseCommitKey = "release.commit"

var frenchMonths = []string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août", "septembre", "octobre", "novembre", "décembre"}

type change struct {
	sha, subject string
	at           time.Time
}

func readChanges(raw string) []change {
	var out []change
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 || strings.TrimSpace(parts[2]) == "" {
			continue
		}
		at, _ := time.Parse(time.RFC3339, parts[1])
		out = append(out, change{sha: parts[0], at: at, subject: strings.TrimSpace(parts[2])})
	}
	return out
}

// technical tells the commits that change nothing for the users.
func technical(subject string) bool {
	lower := strings.ToLower(subject)
	return strings.HasPrefix(subject, "Merge ") || strings.HasPrefix(lower, "ci:") || strings.HasPrefix(lower, "ci ") || strings.Contains(lower, "régénéré")
}

// recordRelease writes, once per new version, the note of what it brings:
// the commits since the version noted before (the last week the first time).
func (s *Server) recordRelease(ctx context.Context) {
	raw, err := os.ReadFile(changesFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			s.logger.Info("no release notes: the list of changes is missing", "file", changesFile)
		} else {
			s.logger.Warn("no release notes: the list of changes cannot be read", "file", changesFile, "error", err)
		}
		s.releaseNoted.Store(true)
		return
	}
	changes := readChanges(string(raw))
	st := s.db.Load()
	if len(changes) == 0 || st == nil {
		if len(changes) == 0 {
			s.logger.Info("no release notes: the list of changes is empty", "file", changesFile)
		}
		s.releaseNoted.Store(len(changes) == 0)
		return
	}
	last, _, err := st.Setting(ctx, releaseCommitKey)
	if err != nil {
		s.logger.Warn("release notes not recorded yet", "error", err)
		return
	}
	if last == changes[0].sha {
		s.releaseNoted.Store(true)
		return
	}
	var lines []string
	for _, c := range changes {
		if c.sha == last || last == "" && time.Since(c.at) > 7*24*time.Hour {
			break
		}
		if !technical(c.subject) {
			lines = append(lines, "- "+c.subject)
		}
	}
	if len(lines) > 0 {
		day := changes[0].at.In(paris())
		r := store.Release{
			Version: version.Version, Title: fmt.Sprintf("Mise à jour du %d %s %d", day.Day(), frenchMonths[day.Month()-1], day.Year()),
			Content: strings.Join(lines, "\n"), CreatedBy: "DocIbis",
		}
		if err := st.SaveRelease(ctx, &r); err != nil {
			s.logger.Warn("release notes not recorded", "error", err)
			return
		}
		s.logger.Info("release notes recorded", "version", r.Version, "changes", len(lines))
	}
	if err := st.SetSetting(ctx, releaseCommitKey, changes[0].sha); err != nil {
		s.logger.Warn("release commit not recorded", "error", err)
		return
	}
	s.releaseNoted.Store(true)
}

func paris() *time.Location {
	if loc, err := time.LoadLocation("Europe/Paris"); err == nil {
		return loc
	}
	return time.UTC
}

func release(r store.Release) Release {
	return Release{Id: r.ID, Version: r.Version, Title: r.Title, Content: r.Content, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// checkRelease reads a note; it returns the reason when refused.
func checkRelease(in *ReleaseInput, r *store.Release) string {
	if in == nil {
		return "Donnez la version, le titre et le texte."
	}
	r.Version, r.Title, r.Content = strings.TrimSpace(in.Version), strings.TrimSpace(in.Title), strings.TrimSpace(in.Content)
	switch {
	case r.Version == "" || len([]rune(r.Version)) > 40:
		return "Version : de 1 à 40 caractères, par exemple 1.4.0."
	case r.Title == "" || len([]rune(r.Title)) > 200:
		return "Titre : de 1 à 200 caractères."
	case r.Content == "" || len(r.Content) > 100000:
		return "Texte : de 1 à 100 000 caractères."
	}
	return ""
}

func (s *Server) ListReleases(ctx context.Context, req ListReleasesRequestObject) (ListReleasesResponseObject, error) {
	rows, total, err := s.db.Load().Releases(ctx, page(req.Params.Limit, req.Params.Offset))
	if err != nil {
		return nil, err
	}
	out := ReleasePage{Items: make([]Release, 0, len(rows)), Total: total}
	for _, r := range rows {
		out.Items = append(out.Items, release(r))
	}
	return ListReleases200JSONResponse(out), nil
}

func (s *Server) CreateRelease(ctx context.Context, req CreateReleaseRequestObject) (CreateReleaseResponseObject, error) {
	me, _ := userFrom(ctx)
	r := store.Release{CreatedBy: me.Username}
	if msg := checkRelease(req.Body, &r); msg != "" {
		return CreateRelease400ApplicationProblemPlusJSONResponse{badRequest(msg)}, nil
	}
	if err := s.db.Load().SaveRelease(ctx, &r); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "release.create", "release:"+r.Version, r.Title)
	return CreateRelease201JSONResponse(release(r)), nil
}

func (s *Server) UpdateRelease(ctx context.Context, req UpdateReleaseRequestObject) (UpdateReleaseResponseObject, error) {
	st := s.db.Load()
	r, ok, err := st.ReleaseByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UpdateRelease404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Note inconnue."))}, nil
	}
	if msg := checkRelease(req.Body, &r); msg != "" {
		return UpdateRelease400ApplicationProblemPlusJSONResponse{badRequest(msg)}, nil
	}
	if err := st.SaveRelease(ctx, &r); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "release.update", "release:"+r.Version, r.Title)
	return UpdateRelease200JSONResponse(release(r)), nil
}

func (s *Server) DeleteRelease(ctx context.Context, req DeleteReleaseRequestObject) (DeleteReleaseResponseObject, error) {
	st := s.db.Load()
	r, ok, err := st.ReleaseByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return DeleteRelease404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Note inconnue."))}, nil
	}
	if err := st.DeleteRelease(ctx, r.ID); err != nil {
		return nil, err
	}
	me, _ := userFrom(ctx)
	s.audit(ctx, me.Username, "release.delete", "release:"+r.Version, fmt.Sprintf("%q", r.Title))
	return DeleteRelease204Response{}, nil
}
