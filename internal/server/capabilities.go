package server

import (
	"encoding/json"
	"net/http"
)

type capabilitiesResponse struct {
	Formats                []string `json:"formats"`
	Features               []string `json:"features"`
	MaxConcurrentDownloads int      `json:"max_concurrent_downloads"`
	SupportsRangeRequests  bool     `json:"supports_range_requests"`
}

func (s *Server) handleCapabilities(w http.ResponseWriter, _ *http.Request) {
	feats := []string{"external_search"}
	if s.deps.EnableAutoMonitoring {
		feats = append(feats, "auto_monitoring")
	}
	resp := capabilitiesResponse{
		Formats:                []string{"epub", "pdf", "mobi", "azw3"},
		Features:               feats,
		MaxConcurrentDownloads: 4,
		SupportsRangeRequests:  true,
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
