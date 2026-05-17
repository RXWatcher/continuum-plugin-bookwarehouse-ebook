// Package consumer implements the event_consumer.v1 handler that processes
// portal-emitted request_submitted events.
package consumer

import (
	"context"
	"fmt"
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
		// Capability servers serve before Configure runs. Nack so the host
		// redelivers once configured instead of acking and dropping the
		// request permanently.
		return nil, fmt.Errorf("plugin not configured yet")
	}
	p := req.GetPayload().AsMap()
	if target := targetPluginIDFromPayload(p); target != d.PluginID {
		return &pluginv1.HandleEventResponse{}, nil
	}
	requestID := requestIDFromPayload(p)
	if requestID == "" {
		return &pluginv1.HandleEventResponse{}, nil
	}
	autoMonitor, _ := p["auto_monitor"].(bool)

	// Must persist: if this row is lost the reconciler never polls it and
	// the request is permanently lost. Nack on failure (the terminal guard
	// in UpsertForwardedRequest makes redelivery idempotent).
	if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID:   requestID,
		Status:      "submitted",
		AutoMonitor: autoMonitor,
		UpdatedAt:   time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("persist submitted %s: %w", requestID, err)
	}

	mreq := bookwarehouse.MonitoringRequest{
		Title:       stringField(p, "title"),
		Authors:     stringSliceField(p, "authors"),
		ISBN:        stringField(p, "isbn"),
		FormatPref:  stringField(p, "format_pref"),
		AutoMonitor: autoMonitor,
	}
	resp, err := d.BW.AddMonitoring(ctx, mreq)
	if err != nil {
		if perr := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
			RequestID:   requestID,
			Status:      "failed",
			ErrorText:   err.Error(),
			AutoMonitor: autoMonitor,
			UpdatedAt:   time.Now(),
		}); perr != nil {
			// Couldn't even record the failure — nack so it's retried
			// rather than lost.
			return nil, fmt.Errorf("persist failed %s: %w", requestID, perr)
		}
		d.Pub.Publish(ctx, "request_failed", map[string]any{
			"request_id":         requestID,
			"requestId":          requestID,
			"provider_plugin_id": d.PluginID,
			"reason":             err.Error(),
		})
		return &pluginv1.HandleEventResponse{}, nil
	}
	// Must persist the external_id: without it the reconciler skips this row
	// forever (it requires a non-empty external_id). Nack on failure.
	if err := d.Store.UpsertForwardedRequest(ctx, store.ForwardedRequest{
		RequestID:   requestID,
		ExternalID:  resp.ID,
		Status:      "acknowledged",
		AutoMonitor: autoMonitor,
		UpdatedAt:   time.Now(),
	}); err != nil {
		return nil, fmt.Errorf("persist acknowledged %s: %w", requestID, err)
	}
	d.Pub.Publish(ctx, "request_acknowledged", map[string]any{
		"request_id":         requestID,
		"requestId":          requestID,
		"external_id":        resp.ID,
		"provider_plugin_id": d.PluginID,
	})
	return &pluginv1.HandleEventResponse{}, nil
}

func targetPluginIDFromPayload(m map[string]any) string {
	for _, key := range []string{"target_plugin_id", "target_provider_plugin_id", "provider_plugin_id"} {
		if v, _ := m[key].(string); v != "" {
			return v
		}
	}
	return ""
}

func requestIDFromPayload(m map[string]any) string {
	if id, _ := m["request_id"].(string); id != "" {
		return id
	}
	id, _ := m["requestId"].(string)
	return id
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
