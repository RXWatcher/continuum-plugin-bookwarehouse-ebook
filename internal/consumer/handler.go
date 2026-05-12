// Package consumer implements the event_consumer.v1 handler that processes
// portal-emitted request_submitted events.
package consumer

import (
	"context"
	"time"

	pluginv1 "github.com/ContinuumApp/continuum-plugin-sdk/pkg/pluginproto/continuum/plugin/v1"

	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/ContinuumApp/continuum-plugin-bookwarehouse-ebook/internal/store"
)

// Publisher is the subset of internal/event.Publisher we need; defined as
// an interface so tests can supply a fake.
type Publisher interface {
	Publish(ctx context.Context, name string, payload map[string]any)
}

type Deps struct {
	Store    *store.Store
	Pub      Publisher
	BW       *bookwarehouse.Client
	PluginID string
}

type Handler struct {
	pluginv1.UnimplementedEventConsumerServer
	depsFn func() *Deps
}

func New(depsFn func() *Deps) *Handler { return &Handler{depsFn: depsFn} }

func (h *Handler) HandleEvent(ctx context.Context, req *pluginv1.HandleEventRequest) (*pluginv1.HandleEventResponse, error) {
	if req.GetEventName() != "plugin.continuum.ebooks.request_submitted" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	if req.GetPayload() == nil {
		return &pluginv1.HandleEventResponse{}, nil
	}
	d := h.depsFn()
	if d == nil {
		return &pluginv1.HandleEventResponse{}, nil
	}
	p := req.GetPayload().AsMap()
	if target, _ := p["target_plugin_id"].(string); target != d.PluginID {
		return &pluginv1.HandleEventResponse{}, nil
	}
	requestID, _ := p["request_id"].(string)
	if requestID == "" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	autoMonitor, _ := p["auto_monitor"].(bool)

	_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID:   requestID,
		Status:      "submitted",
		AutoMonitor: autoMonitor,
		UpdatedAt:   time.Now(),
	})

	mreq := bookwarehouse.MonitoringRequest{
		Title:       stringField(p, "title"),
		Authors:     stringSliceField(p, "authors"),
		ISBN:        stringField(p, "isbn"),
		FormatPref:  stringField(p, "format_pref"),
		AutoMonitor: autoMonitor,
	}
	resp, err := d.BW.AddMonitoring(ctx, mreq)
	if err != nil {
		_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID:   requestID,
			Status:      "failed",
			ErrorText:   err.Error(),
			AutoMonitor: autoMonitor,
			UpdatedAt:   time.Now(),
		})
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id": requestID,
			"reason":     err.Error(),
		})
		return &pluginv1.HandleEventResponse{}, nil
	}
	_ = d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID:   requestID,
		ExternalID:  resp.ID,
		Status:      "acknowledged",
		AutoMonitor: autoMonitor,
		UpdatedAt:   time.Now(),
	})
	d.Pub.Publish(ctx, "request_acknowledged", map[string]any{
		"request_id":  requestID,
		"external_id": resp.ID,
	})
	return &pluginv1.HandleEventResponse{}, nil
}

func stringField(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func stringSliceField(m map[string]any, k string) []string {
	v, ok := m[k].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(v))
	for _, e := range v {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
