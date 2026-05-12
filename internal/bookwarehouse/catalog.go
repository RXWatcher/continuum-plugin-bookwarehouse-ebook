package bookwarehouse

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
)

// ListParams is the query shape for ListBooks.
type ListParams struct {
	Cursor string
	Limit  int
	Sort   string
	Order  string
	Query  string // if set, hits /books/search
}

func (c *Client) ListBooks(ctx context.Context, p ListParams) (Paged[Book], error) {
	q := url.Values{}
	if p.Cursor != "" {
		q.Set("cursor", p.Cursor)
	}
	if p.Limit > 0 {
		q.Set("limit", strconv.Itoa(p.Limit))
	}
	if p.Sort != "" {
		q.Set("sort", p.Sort)
	}
	if p.Order != "" {
		q.Set("order", p.Order)
	}
	path := "/api/v1/books"
	if p.Query != "" {
		q.Set("q", p.Query)
		path = "/api/v1/books/search"
	}
	body, err := c.Get(ctx, path+"?"+q.Encode())
	if err != nil {
		return Paged[Book]{}, err
	}
	var out Paged[Book]
	if err := json.Unmarshal(body, &out); err != nil {
		return Paged[Book]{}, fmt.Errorf("decode books: %w", err)
	}
	return out, nil
}

func (c *Client) GetBook(ctx context.Context, id string) (BookDetail, error) {
	body, err := c.Get(ctx, "/api/v1/books/"+url.PathEscape(id))
	if err != nil {
		return BookDetail{}, err
	}
	var out BookDetail
	if err := json.Unmarshal(body, &out); err != nil {
		return BookDetail{}, fmt.Errorf("decode book: %w", err)
	}
	return out, nil
}

// FileURL returns the upstream URL for fetching a specific format of a book.
func (c *Client) FileURL(bookID, format string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/files/%s", c.baseURL, url.PathEscape(bookID), url.PathEscape(format))
}

// CoverURL returns the deterministic upstream URL for a cover at a given size.
func (c *Client) CoverURL(bookID, size string) string {
	return fmt.Sprintf("%s/api/v1/books/%s/cover/%s", c.baseURL, url.PathEscape(bookID), url.PathEscape(size))
}
