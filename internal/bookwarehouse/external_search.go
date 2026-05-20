package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

// ExternalSearch hits BW's wrapper that aggregates Open Library + Google Books.
func (c *Client) ExternalSearch(ctx context.Context, query string, limit int) ([]ExternalSearchHit, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	body, err := c.Get(ctx, "/api/v1/search/external?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var env struct {
		Results []struct {
			Title         string   `json:"title"`
			Authors       []string `json:"authors,omitempty"`
			PublishedDate *string  `json:"published_date,omitempty"`
			Language      *string  `json:"language,omitempty"`
			CoverURL      *string  `json:"cover_url,omitempty"`
			Source        string   `json:"source"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode external_search: %w", err)
	}
	out := make([]ExternalSearchHit, 0, len(env.Results))
	for _, item := range env.Results {
		hit := ExternalSearchHit{
			Title:   item.Title,
			Authors: item.Authors,
			Source:  item.Source,
		}
		if item.Language != nil {
			hit.Language = *item.Language
		}
		if item.CoverURL != nil {
			hit.CoverURL = *item.CoverURL
		}
		if item.PublishedDate != nil {
			hit.Year = extractYear(*item.PublishedDate)
		}
		out = append(out, hit)
	}
	return out, nil
}

var yearPrefix = regexp.MustCompile(`\b(\d{4})\b`)

func extractYear(s string) int {
	m := yearPrefix.FindStringSubmatch(s)
	if len(m) != 2 {
		return 0
	}
	year, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return year
}
