package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
)

// MonitoringRequest is the payload BookWarehouse expects for adding a book to
// its monitoring/download queue.
type MonitoringRequest struct {
	Title          string   `json:"title"`
	Authors        []string `json:"authors,omitempty"`
	ISBN           string   `json:"isbn,omitempty"`
	FormatPref     string   `json:"format_pref,omitempty"`
	QualityProfile string   `json:"quality_profile,omitempty"`
	AutoMonitor    bool     `json:"auto_monitor,omitempty"`
}

// MonitoringResponse is BookWarehouse's response after queueing a request.
type MonitoringResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func (c *Client) AddMonitoring(ctx context.Context, r MonitoringRequest) (MonitoringResponse, error) {
	body, err := json.Marshal(r)
	if err != nil {
		return MonitoringResponse{}, fmt.Errorf("encode monitoring request: %w", err)
	}
	respBody, err := c.PostJSON(ctx, "/api/v1/monitoring/add", body)
	if err != nil {
		return MonitoringResponse{}, err
	}
	var out MonitoringResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return MonitoringResponse{}, fmt.Errorf("decode monitoring response: %w", err)
	}
	return out, nil
}

func (c *Client) GetMonitoring(ctx context.Context, externalID string) (MonitoringResponse, error) {
	respBody, err := c.Get(ctx, "/api/v1/monitoring/"+externalID)
	if err != nil {
		return MonitoringResponse{}, err
	}
	var out MonitoringResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return MonitoringResponse{}, fmt.Errorf("decode monitoring snapshot: %w", err)
	}
	return out, nil
}
