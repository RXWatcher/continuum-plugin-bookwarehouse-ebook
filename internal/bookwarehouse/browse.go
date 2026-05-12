package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

func (c *Client) ListAuthors(ctx context.Context, cursor string, limit int) (Paged[Author], error) {
	return listBrowse[Author](ctx, c, "/api/v1/authors", cursor, limit)
}

func (c *Client) ListSeries(ctx context.Context, cursor string, limit int) (Paged[Series], error) {
	return listBrowse[Series](ctx, c, "/api/v1/series", cursor, limit)
}

func (c *Client) ListGenres(ctx context.Context, cursor string, limit int) (Paged[Genre], error) {
	return listBrowse[Genre](ctx, c, "/api/v1/genres", cursor, limit)
}

func (c *Client) ListTags(ctx context.Context, cursor string, limit int) (Paged[Tag], error) {
	return listBrowse[Tag](ctx, c, "/api/v1/tags", cursor, limit)
}

func listBrowse[T any](ctx context.Context, c *Client, path, cursor string, limit int) (Paged[T], error) {
	q := url.Values{}
	if cursor != "" {
		q.Set("cursor", cursor)
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	body, err := c.Get(ctx, path+"?"+q.Encode())
	if err != nil {
		return Paged[T]{}, err
	}
	var out Paged[T]
	if err := json.Unmarshal(body, &out); err != nil {
		return Paged[T]{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return out, nil
}
