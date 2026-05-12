// Package reconciler periodically polls upstream BookWarehouse for status of
// non-terminal forwarded_request rows and emits status events.
package reconciler

import (
	"context"
	"time"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/store"
)

type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store *store.Store
	Pub   Publisher
	BW    *bookwarehouse.Client
}

type Reconciler struct {
	deps Deps
}

func New(d Deps) *Reconciler { return &Reconciler{deps: d} }

// Tick processes all non-terminal forwarded_request rows once.
func (r *Reconciler) Tick(ctx context.Context) error {
	rows, err := r.deps.Store.ListNonTerminal(ctx, 200)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.ExternalID == "" {
			continue
		}
		snap, err := r.deps.BW.GetMonitoring(ctx, row.ExternalID)
		if err != nil {
			_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID:  row.RequestID,
				ExternalID: row.ExternalID,
				Status:     row.Status,
				LastPolled: time.Now(),
				ErrorText:  err.Error(),
				UpdatedAt:  time.Now(),
			})
			continue
		}
		newStatus := translateStatus(snap.Status)
		if newStatus == row.Status {
			_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID:  row.RequestID,
				ExternalID: row.ExternalID,
				Status:     row.Status,
				LastPolled: time.Now(),
				UpdatedAt:  time.Now(),
			})
			continue
		}
		_ = r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID:  row.RequestID,
			ExternalID: row.ExternalID,
			Status:     newStatus,
			LastPolled: time.Now(),
			UpdatedAt:  time.Now(),
		})
		switch newStatus {
		case "imported":
			r.deps.Pub.Publish(ctx, "request_fulfilled", map[string]any{
				"request_id":        row.RequestID,
				"external_id":       row.ExternalID,
				"fulfilled_book_id": snap.ID,
			})
		case "failed":
			r.deps.Pub.Publish(ctx, "request_failed", map[string]any{
				"request_id":  row.RequestID,
				"external_id": row.ExternalID,
				"reason":      "upstream marked failed",
			})
		default:
			r.deps.Pub.Publish(ctx, "request_status_changed", map[string]any{
				"request_id":  row.RequestID,
				"external_id": row.ExternalID,
				"status":      newStatus,
			})
		}
	}
	return nil
}

// translateStatus maps BookWarehouse's status strings to portal-facing statuses.
func translateStatus(bwStatus string) string {
	switch bwStatus {
	case "queued", "monitoring":
		return "searching"
	case "found":
		return "found"
	case "downloading", "grabbing":
		return "downloading"
	case "imported", "completed":
		return "imported"
	case "failed", "error":
		return "failed"
	}
	return "acknowledged"
}
