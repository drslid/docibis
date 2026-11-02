package store

import (
	"context"
	"time"

	"github.com/uptrace/bun"
)

// AlertEvent is an alert raised or resolved, with who was told.
type AlertEvent struct {
	bun.BaseModel `bun:"table:alert_events,alias:ae"`
	ID            int64     `bun:"id,pk,autoincrement"`
	CreatedAt     time.Time `bun:"created_at,notnull"`
	Rule          string    `bun:"rule,notnull"`
	State         string    `bun:"state,notnull"`
	Severity      string    `bun:"severity,notnull"`
	Value         float64   `bun:"value,notnull"`
	Threshold     float64   `bun:"threshold,notnull"`
	Unit          string    `bun:"unit,notnull"`
	Notified      string    `bun:"notified,notnull"`
}

// RecordAlertEvent adds an event to the history of the alerts.
func (s *Store) RecordAlertEvent(ctx context.Context, e *AlertEvent) error {
	e.CreatedAt = time.Now().UTC()
	_, err := s.DB.NewInsert().Model(e).Exec(ctx)
	return err
}

// AlertEvents returns the latest events, the newest first.
func (s *Store) AlertEvents(ctx context.Context, limit int) ([]AlertEvent, error) {
	rows := []AlertEvent{}
	err := s.DB.NewSelect().Model(&rows).Order("id DESC").Limit(int64(limit)).Scan(ctx)
	return rows, err
}
