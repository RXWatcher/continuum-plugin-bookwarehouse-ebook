// Package request implements the snapshot endpoint
// GET /api/v1/requests/{external_id} which returns a freshly-fetched status
// from the upstream BookWarehouse service. The portal calls this to refresh a
// single request on-demand (the scheduled reconciler does it on a 1-minute
// cadence; this endpoint covers user "refresh status" actions).
package request

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/RXWatcher/silo-plugin-bookwarehouse-ebook/internal/bookwarehouse"
)

type Handler struct {
	client *bookwarehouse.Client
}

func NewHandler(c *bookwarehouse.Client) *Handler { return &Handler{client: c} }

func (h *Handler) Snapshot() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		eid := r.PathValue("external_id")
		if eid == "" {
			http.Error(w, "external_id required", http.StatusBadRequest)
			return
		}
		snap, err := h.client.GetMonitoring(r.Context(), eid)
		if err != nil {
			// The upstream error embeds up to 512 B of the upstream body
			// (and the transport error wraps the internal base URL); log it
			// server-side, return a generic message to the client.
			slog.Error("request snapshot upstream error", "external_id", eid, "err", err)
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
