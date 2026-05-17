// Package bookwarehouse is a typed HTTP client for the upstream BookWarehouse
// (Calibre-backed) service. Mirrors /opt/librarymanagerre/lib/bookwarehouse/client.ts.
package bookwarehouse

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

const defaultTimeout = 30 * time.Second

// maxResponseBytes caps the body read from the upstream BookWarehouse
// service. Normal JSON list/detail responses are well under this; the cap
// defends against memory exhaustion if the upstream returns a runaway
// body (broken, compromised, hostile).
const maxResponseBytes = 10 << 20 // 10 MiB

// errBodySnippet caps how much of an upstream error body we inline into an
// error string. The body can be up to maxResponseBytes and the error
// propagates into logs and responses, so embedding it whole is a hazard.
const errBodySnippet = 512

func truncForError(b []byte) string {
	if len(b) <= errBodySnippet {
		return string(b)
	}
	return string(b[:errBodySnippet]) + "…(truncated)"
}

// defaultCoverSizeFallback is the manifest-documented default for the
// default_cover_size config key ("Defaults to large").
const defaultCoverSizeFallback = "large"

func normalizeCoverSize(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "small", "medium", "large", "original":
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return defaultCoverSizeFallback
	}
}

type Client struct {
	baseURL string
	apiKey  string
	hc      *http.Client
	// defaultCoverSize is written by SetDefaultCoverSize (from Configure) and
	// read concurrently by in-flight catalog requests, so it is atomic.
	defaultCoverSize atomic.Pointer[string]
}

func NewClient(baseURL, apiKey string) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		hc: &http.Client{
			Timeout: defaultTimeout,
			// X-API-Key is a custom header, so Go's default redirect logic
			// (which only strips Authorization/Cookie/WWW-Auth cross-host)
			// would forward the upstream credential to a redirect target.
			// Strip it on any cross-host hop and cap the chain.
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 10 {
					return http.ErrUseLastResponse
				}
				if req.URL.Host != via[0].URL.Host {
					req.Header.Del("X-API-Key")
				}
				return nil
			},
		},
	}
	def := defaultCoverSizeFallback
	c.defaultCoverSize.Store(&def)
	return c
}

// SetDefaultCoverSize sets the cover size used when building portal-relative
// cover URLs. Invalid/empty values fall back to the documented default
// ("large"). Called from Configure so operator changes take effect.
func (c *Client) SetDefaultCoverSize(size string) {
	v := normalizeCoverSize(size)
	c.defaultCoverSize.Store(&v)
}

// coverSize returns the configured default cover size.
func (c *Client) coverSize() string { return *c.defaultCoverSize.Load() }

func (c *Client) BaseURL() string { return c.baseURL }

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Get(ctx, "/api/v1/health")
	if err == nil {
		return nil
	}
	_, err = c.Get(ctx, "/health")
	return err
}

func (c *Client) Get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncForError(body))
	}
	return body, nil
}

// GetStream issues a GET and returns the response so the caller can copy the
// body without buffering it in memory. Used for cover images and ebook files
// where the response can be megabytes. Caller MUST close resp.Body.
func (c *Client) GetStream(ctx context.Context, path string) (*http.Response, error) {
	return c.GetStreamWithRange(ctx, path, "")
}

// GetStreamWithRange is GetStream that also forwards the caller's Range
// request header, so byte-range (seek/resume) requests reach upstream and the
// 206 Partial Content response passes back through. Caller MUST close
// resp.Body. A 416 (range not satisfiable) is returned to the caller as a
// normal response, not an error, so it can be relayed verbatim.
func (c *Client) GetStreamWithRange(ctx context.Context, path, rangeHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		resp.Body.Close()
		return nil, fmt.Errorf("upstream %d", resp.StatusCode)
	}
	return resp, nil
}

func (c *Client) PostJSON(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream %d: %s", resp.StatusCode, truncForError(respBody))
	}
	return respBody, nil
}
