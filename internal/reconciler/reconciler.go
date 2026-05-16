// Package reconciler periodically polls upstream BookWarehouse for status of
// non-terminal forwarded_request rows and emits status events.
package reconciler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/store"
)

// tickTimeout caps a full Tick invocation. The scheduler fires this task on
// a 1-minute cron; capping below that prevents the next tick from arriving
// while we're still working and avoids starving other scheduled tasks if
// the upstream BookWarehouse hangs.
const tickTimeout = 45 * time.Second

// perRowTimeout caps each upstream lookup. We process up to 200 rows per
// tick; 1s per row × 200 + slack fits comfortably inside tickTimeout.
const perRowTimeout = 10 * time.Second

type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store    *store.Store
	Pub      Publisher
	BW       *bookwarehouse.Client
	PluginID string
}

type Reconciler struct {
	deps Deps

	// tickMu guards against overlapping Tick calls. The SDK scheduler is
	// generally serial, but a slow upstream + clock skew can occasionally
	// trigger overlap; in that case the second call is dropped rather than
	// doubling up on upstream calls and DB writes.
	tickMu sync.Mutex
}

func New(d Deps) *Reconciler { return &Reconciler{deps: d} }

// Tick processes all non-terminal forwarded_request rows once.
func (r *Reconciler) Tick(ctx context.Context) error {
	if !r.tickMu.TryLock() {
		return nil
	}
	defer r.tickMu.Unlock()

	ctx, cancel := context.WithTimeout(ctx, tickTimeout)
	defer cancel()

	rows, err := r.deps.Store.ListNonTerminal(ctx, 200)
	if err != nil {
		return err
	}
	// firstErr captures the first DB write failure so we still process the
	// remaining rows (one dead row shouldn't starve the others) but the SDK
	// records a failed tick at the end.
	var firstErr error
	for _, row := range rows {
		if row.ExternalID == "" {
			continue
		}
		rowCtx, rowCancel := context.WithTimeout(ctx, perRowTimeout)
		snap, err := r.deps.BW.GetMonitoring(rowCtx, row.ExternalID)
		rowCancel()
		if err != nil {
			if uerr := r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID:  row.RequestID,
				ExternalID: row.ExternalID,
				Status:     row.Status,
				LastPolled: time.Now(),
				ErrorText:  err.Error(),
				UpdatedAt:  time.Now(),
			}); uerr != nil && firstErr == nil {
				firstErr = fmt.Errorf("upsert (after upstream err): %w", uerr)
			}
			continue
		}
		newStatus := translateStatus(snap.Status)
		if newStatus == row.Status {
			if uerr := r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
				RequestID:  row.RequestID,
				ExternalID: row.ExternalID,
				Status:     row.Status,
				LastPolled: time.Now(),
				UpdatedAt:  time.Now(),
			}); uerr != nil && firstErr == nil {
				firstErr = fmt.Errorf("upsert (same status): %w", uerr)
			}
			continue
		}
		if uerr := r.deps.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID:  row.RequestID,
			ExternalID: row.ExternalID,
			Status:     newStatus,
			LastPolled: time.Now(),
			UpdatedAt:  time.Now(),
		}); uerr != nil && firstErr == nil {
			firstErr = fmt.Errorf("upsert (status change): %w", uerr)
		}
		switch newStatus {
		case "imported":
			r.deps.Pub.Publish(ctx, "request_fulfilled", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"fulfilled_book_id":  snap.ID,
				"provider_plugin_id": r.deps.PluginID,
			})
		case "failed":
			r.deps.Pub.Publish(ctx, "request_failed", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"provider_plugin_id": r.deps.PluginID,
				"reason":             "upstream marked failed",
			})
		default:
			r.deps.Pub.Publish(ctx, "request_status_changed", map[string]any{
				"request_id":         row.RequestID,
				"requestId":          row.RequestID,
				"external_id":        row.ExternalID,
				"provider_plugin_id": r.deps.PluginID,
				"status":             newStatus,
			})
		}
	}
	return firstErr
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
