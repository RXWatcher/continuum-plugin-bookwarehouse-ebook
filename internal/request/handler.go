// Package request implements the snapshot endpoint
// GET /api/v1/requests/{external_id} which returns a freshly-fetched status
// from the upstream BookWarehouse service. The portal calls this to refresh a
// single request on-demand (the scheduled reconciler does it on a 1-minute
// cadence; this endpoint covers user "refresh status" actions).
package request

import (
	"encoding/json"
	"net/http"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
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
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"external_id": snap.ID,
			"status":      snap.Status,
		})
	}
}
