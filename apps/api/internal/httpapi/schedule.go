package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// A version can be published at a date (a new tariff on the 1st of the
// month): the server publishes it within the minute.

func (s *Server) ScheduleTemplate(ctx context.Context, req ScheduleTemplateRequestObject) (ScheduleTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ScheduleTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	now := time.Now()
	switch {
	case req.Body == nil || req.Body.Version < 1 || req.Body.Version > t.Versions:
		return ScheduleTemplate400ApplicationProblemPlusJSONResponse{badRequest(fmt.Sprintf("Version à publier : de 1 à %d.", t.Versions))}, nil
	case req.Body.Version == t.Published:
		return ScheduleTemplate400ApplicationProblemPlusJSONResponse{badRequest("Cette version est déjà publiée.")}, nil
	case !req.Body.At.After(now):
		return ScheduleTemplate400ApplicationProblemPlusJSONResponse{badRequest("Choisissez une date à venir.")}, nil
	case req.Body.At.After(now.AddDate(1, 0, 0)):
		return ScheduleTemplate400ApplicationProblemPlusJSONResponse{badRequest("Choisissez une date dans l'année qui vient.")}, nil
	}
	me, _ := userFrom(ctx)
	at := req.Body.At.UTC().Truncate(time.Minute)
	if err := st.ScheduleTemplate(ctx, t.ID, req.Body.Version, &at, me.Username); err != nil {
		return nil, err
	}
	s.audit(ctx, me.Username, "template.schedule", "template:"+t.Path, fmt.Sprintf("version %d le %s", req.Body.Version, at.Format(time.RFC3339)))
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return ScheduleTemplate200JSONResponse(out), nil
}

func (s *Server) UnscheduleTemplate(ctx context.Context, req UnscheduleTemplateRequestObject) (UnscheduleTemplateResponseObject, error) {
	st := s.db.Load()
	t, ok, err := st.TemplateByID(ctx, req.Id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return UnscheduleTemplate404ApplicationProblemPlusJSONResponse{NotFoundApplicationProblemPlusJSONResponse(problem(http.StatusNotFound, "Modèle inconnu."))}, nil
	}
	if t.ScheduledVersion > 0 {
		if err := st.ScheduleTemplate(ctx, t.ID, 0, nil, ""); err != nil {
			return nil, err
		}
		me, _ := userFrom(ctx)
		s.audit(ctx, me.Username, "template.unschedule", "template:"+t.Path, fmt.Sprintf("version %d", t.ScheduledVersion))
	}
	out, _, err := s.detail(ctx, t.ID)
	if err != nil {
		return nil, err
	}
	return UnscheduleTemplate200JSONResponse(out), nil
}

// WatchSchedules publishes the planned versions whose date has come, every
// 30 seconds until ctx ends. It also writes the release note of this version
// when the start could not, and every 10 minutes reads and qualifies the
// templates left aside (LibreOffice busy, for instance).
func (s *Server) WatchSchedules(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.publishDue(ctx)
			if !s.releaseNoted.Load() {
				s.recordRelease(ctx)
			}
			if n%20 == 0 && s.renderer.Load() != nil {
				s.describeLater(ctx)
			}
		}
	}
}

func (s *Server) publishDue(ctx context.Context) {
	st := s.db.Load()
	if st == nil || st.Replication().LeaseLost {
		return
	}
	due, err := st.DueSchedules(ctx, time.Now())
	if err != nil {
		s.logger.Warn("cannot read the planned publications", "error", err)
		return
	}
	for _, t := range due {
		done, err := st.PublishDue(ctx, t.ID, t.ScheduledVersion)
		if err != nil {
			s.logger.Warn("planned publication failed", "template", t.Path, "version", t.ScheduledVersion, "error", err)
			continue
		}
		if done {
			s.describeLater(context.WithoutCancel(ctx))
			s.audit(ctx, t.ScheduledBy, "template.publish", "template:"+t.Path,
				fmt.Sprintf("version %d (avant : %d), publication programmée", t.ScheduledVersion, t.Published))
		}
	}
}
