package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// Upstream BookWarehouse only exposes /api/v1/genres for facet browsing
// (see /opt/librarymanagerre/lib/bookwarehouse/openapi.json — there is no
// /authors, /series, or /tags endpoint). We return empty pages for the
// missing endpoints so the SPA's Authors/Series pages can still render an
// "empty" state rather than a 502.

func (c *Client) ListAuthors(_ context.Context, _ string, _ int) (Paged[Author], error) {
	return Paged[Author]{Items: []Author{}}, nil
}

func (c *Client) ListSeries(_ context.Context, _ string, _ int) (Paged[Series], error) {
	return Paged[Series]{Items: []Series{}}, nil
}

func (c *Client) ListTags(_ context.Context, _ string, _ int) (Paged[Tag], error) {
	return Paged[Tag]{Items: []Tag{}}, nil
}

func (c *Client) ListGenres(ctx context.Context, cursor string, limit int) (Paged[Genre], error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	body, err := c.Get(ctx, "/api/v1/genres?"+q.Encode())
	if err != nil {
		return Paged[Genre]{Items: []Genre{}}, err
	}
	// Upstream: {"genres":[{"id":number,"name","slug","book_count"}], "pagination":...}
	var ub struct {
		Genres []struct {
			ID        int    `json:"id"`
			Name      string `json:"name"`
			Slug      string `json:"slug"`
			BookCount int    `json:"book_count"`
		} `json:"genres"`
		Pagination struct {
			Page       int `json:"page"`
			TotalPages int `json:"total_pages"`
			TotalItems int `json:"total_items"`
		} `json:"pagination"`
	}
	if err := json.Unmarshal(body, &ub); err != nil {
		return Paged[Genre]{Items: []Genre{}}, fmt.Errorf("decode genres: %w", err)
	}
	out := Paged[Genre]{
		Total: ub.Pagination.TotalItems,
		Items: make([]Genre, 0, len(ub.Genres)),
	}
	if ub.Pagination.Page < ub.Pagination.TotalPages {
		out.NextCursor = strconv.Itoa(ub.Pagination.Page + 1)
	}
	for _, g := range ub.Genres {
		out.Items = append(out.Items, Genre{
			ID:    strconv.Itoa(g.ID),
			Name:  g.Name,
			Slug:  g.Slug,
			Count: g.BookCount,
		})
	}
	return out, nil
}
