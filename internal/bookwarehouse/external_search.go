package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ExternalSearch hits BW's wrapper that aggregates Open Library + Google Books.
func (c *Client) ExternalSearch(ctx context.Context, query string, limit int) ([]ExternalSearchHit, error) {
	q := url.Values{}
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	body, err := c.Get(ctx, "/api/v1/external_search?"+q.Encode())
	if err != nil {
		return nil, err
	}
	var env struct {
		Items []ExternalSearchHit `json:"items"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decode external_search: %w", err)
	}
	return env.Items, nil
}
