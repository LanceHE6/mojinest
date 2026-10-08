package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Item mirrors the server's item payload (only the fields the client needs).
type Item struct {
	ID         int64  `json:"id"`
	Sha256     string `json:"sha256"`
	Ext        string `json:"ext"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	Name       string `json:"name"`
	Note       string `json:"note"`
	Animated   bool   `json:"animated"`
	Frames     int    `json:"frames"`
	UseCount   int64  `json:"use_count"`
	AIDesc     string `json:"ai_description"`
	AIOCR      string `json:"ai_ocr"`
	Tags       []Tag  `json:"tags"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
	SourcePlat string `json:"source_platform"`
}

// Tag is a tag plus its origin.
type Tag struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// Client talks to a Mojinest server.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

// NewClient builds an API client.
func NewClient(baseURL, token string) *Client {
	httpClient := &http.Client{
		Timeout: 60 * time.Second,
		// Never let a redirect carry credentials to another endpoint: Go
		// forwards Authorization/Cookie when only the port differs (same
		// hostname), so drop them by hand whenever host:port changes. S3
		// presigned redirects keep working because they are self-signed.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return fmt.Errorf("too many redirects")
			}
			if len(via) > 0 && !sameEndpoint(via[0].URL, req.URL) {
				req.Header.Del("Authorization")
				req.Header.Del("Cookie")
			}
			return nil
		},
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    httpClient,
	}
}

// sameEndpoint reports whether two URLs point at the same host and port.
func sameEndpoint(a, b *url.URL) bool {
	return strings.EqualFold(a.Host, b.Host) &&
		strings.EqualFold(schemeWithDefaultPort(a), schemeWithDefaultPort(b))
}

func schemeWithDefaultPort(u *url.URL) string {
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return u.Scheme + ":" + port
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	return c.HTTP.Do(req)
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d: %s", path, resp.StatusCode, apiError(raw, resp.StatusCode))
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

func apiError(raw []byte, code int) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != "" {
		return e.Error
	}
	if code == http.StatusUnauthorized {
		return "unauthorized（检查 Token）"
	}
	return strings.TrimSpace(string(raw))
}

// SearchQuery describes a library query for a query-first command such as copy.
type SearchQuery struct {
	Q       string
	Tags    []string
	Sort    string
	Limit   int
	Animate string // "", "1" (animated only)
}

// Search lists items, defaulting to 50 results sorted by usage.
func (c *Client) Search(ctx context.Context, q SearchQuery) ([]Item, error) {
	v := url.Values{}
	if q.Q != "" {
		v.Set("q", q.Q)
	}
	if len(q.Tags) > 0 {
		v.Set("tags", strings.Join(q.Tags, ","))
	}
	if q.Sort != "" {
		v.Set("sort", q.Sort)
	}
	if q.Animate != "" {
		v.Set("animated", q.Animate)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	v.Set("limit", strconv.Itoa(limit))

	var out struct {
		Items []Item `json:"items"`
		Total int    `json:"total"`
	}
	if err := c.getJSON(ctx, "/api/items?"+v.Encode(), &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// Stats summarises the library on the server.
func (c *Client) Stats(ctx context.Context) (map[string]int64, error) {
	var out map[string]int64
	if err := c.getJSON(ctx, "/api/stats", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Health checks the server is reachable and reports the storage backend.
func (c *Client) Health(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	if err := c.getJSON(ctx, "/healthz", &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Download fetches the original bytes of an item.
func (c *Client) Download(ctx context.Context, it Item) ([]byte, error) {
	path := "/api/files/" + it.Sha256 + "?ext=" + url.QueryEscape(it.Ext)
	resp, err := c.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s: HTTP %d: %s", it.Sha256[:8], resp.StatusCode, apiError(raw, resp.StatusCode))
	}
	return io.ReadAll(io.LimitReader(resp.Body, 64<<20))
}

// MarkUsed records a copy so the library can rank by habit.
func (c *Client) MarkUsed(ctx context.Context, id int64) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/items/"+strconv.FormatInt(id, 10)+"/use", nil, "")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("mark used: HTTP %d: %s", resp.StatusCode, apiError(raw, resp.StatusCode))
	}
	return nil
}

// Ping verifies credentials by hitting an authenticated endpoint.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Stats(ctx)
	return err
}

// Upload sends local files to the library (used by importers).
func (c *Client) Upload(ctx context.Context, files map[string][]byte, tags []string, platform string) (int, error) {
	var body bytes.Buffer
	w := newMultipart(&body)
	for name, data := range files {
		if err := w.File("file", name, data); err != nil {
			return 0, err
		}
	}
	if len(tags) > 0 {
		_ = w.Field("tags", strings.Join(tags, ","))
	}
	if platform != "" {
		_ = w.Field("platform", platform)
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/items", &body, w.ContentType())
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("upload: HTTP %d: %s", resp.StatusCode, apiError(raw, resp.StatusCode))
	}
	var out struct {
		Items []Item `json:"items"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, err
	}
	return len(out.Items), nil
}
