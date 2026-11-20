package pocketbase

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
	"sync"
	"time"
)

const maxResponseBytes = 4 << 20

type Client struct {
	baseURL    string
	httpClient *http.Client

	mu        sync.RWMutex
	authToken string
	email     string
	password  string
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

type AuthResponse struct {
	Token  string `json:"token"`
	Record struct {
		ID    string `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	} `json:"record"`
}

// Authenticate logs in as a superuser and remembers the credentials so the
// client can transparently re-authenticate when the token expires.
func (c *Client) Authenticate(ctx context.Context, email, password string) error {
	if email == "" || password == "" {
		return fmt.Errorf("PocketBase email and password are required")
	}

	c.mu.Lock()
	c.email = email
	c.password = password
	c.mu.Unlock()

	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) error {
	c.mu.RLock()
	email, password := c.email, c.password
	c.mu.RUnlock()

	payload, err := json.Marshal(map[string]string{
		"identity": email,
		"password": password,
	})
	if err != nil {
		return fmt.Errorf("encode authentication request: %w", err)
	}

	// In PocketBase v0.23+, admin accounts live in the _superusers system
	// auth collection. Use /api/collections/users/auth-with-password instead
	// for a regular users-collection account.
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/collections/_superusers/auth-with-password",
		bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create authentication request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("authenticate with PocketBase: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf(
			"PocketBase authentication failed (status %d): %s",
			resp.StatusCode,
			string(body),
		)
	}

	var result AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode authentication response: %w", err)
	}

	if result.Token == "" {
		return fmt.Errorf("PocketBase returned an empty authentication token")
	}

	c.mu.Lock()
	c.authToken = result.Token
	c.mu.Unlock()

	return nil
}

// getJSON performs an authenticated GET and decodes the response into out.
// On a 401 it re-authenticates once and retries.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	for attempt := range 2 {
		status, body, err := c.doGet(ctx, path, query)
		if err != nil {
			return err
		}

		if status == http.StatusUnauthorized && attempt == 0 {
			if err := c.login(ctx); err != nil {
				return fmt.Errorf("re-authenticate with PocketBase: %w", err)
			}
			continue
		}

		if status != http.StatusOK {
			return fmt.Errorf("PocketBase returned status %d: %s", status, truncate(body, 500))
		}

		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decode PocketBase response: %w", err)
		}
		return nil
	}

	return fmt.Errorf("PocketBase request unauthorized after re-authentication")
}

func (c *Client) doGet(ctx context.Context, path string, query url.Values) (int, []byte, error) {
	endpoint := c.baseURL + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, nil, fmt.Errorf("create PocketBase request: %w", err)
	}

	c.mu.RLock()
	token := c.authToken
	c.mu.RUnlock()

	if token == "" {
		return 0, nil, fmt.Errorf("PocketBase client is not authenticated")
	}

	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("send PocketBase request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("read PocketBase response: %w", err)
	}

	return resp.StatusCode, body, nil
}

// ListOptions controls a record list request.
type ListOptions struct {
	Filter  string
	Sort    string
	Fields  string
	Expand  string
	Page    int
	PerPage int
	// SkipTotal skips the (potentially expensive) total count query.
	SkipTotal bool
}

// ListResult is a page of records.
type ListResult struct {
	Page       int              `json:"page"`
	PerPage    int              `json:"perPage"`
	TotalItems int              `json:"totalItems"`
	TotalPages int              `json:"totalPages"`
	Items      []map[string]any `json:"items"`
}

// ListRecords returns a page of records from a collection.
func (c *Client) ListRecords(ctx context.Context, collection string, opts ListOptions) (*ListResult, error) {
	query := url.Values{}
	if opts.Filter != "" {
		query.Set("filter", opts.Filter)
	}
	if opts.Sort != "" {
		query.Set("sort", opts.Sort)
	}
	if opts.Fields != "" {
		query.Set("fields", opts.Fields)
	}
	if opts.Expand != "" {
		query.Set("expand", opts.Expand)
	}
	if opts.Page > 0 {
		query.Set("page", strconv.Itoa(opts.Page))
	}
	if opts.PerPage > 0 {
		query.Set("perPage", strconv.Itoa(opts.PerPage))
	}
	if opts.SkipTotal {
		query.Set("skipTotal", "true")
	}

	var result ListResult
	path := "/api/collections/" + url.PathEscape(collection) + "/records"
	if err := c.getJSON(ctx, path, query, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListAll pages through every record matching opts (ignoring Page/PerPage)
// up to max records. The bool result reports whether results were truncated.
func (c *Client) ListAll(ctx context.Context, collection string, opts ListOptions, max int) ([]map[string]any, bool, error) {
	const perPage = 500

	opts.PerPage = perPage
	opts.SkipTotal = true
	if opts.Sort == "" {
		opts.Sort = "id" // stable ordering across pages
	}

	var all []map[string]any
	for page := 1; ; page++ {
		opts.Page = page

		result, err := c.ListRecords(ctx, collection, opts)
		if err != nil {
			return nil, false, err
		}

		all = append(all, result.Items...)

		if len(all) >= max {
			return all[:max], true, nil
		}
		if len(result.Items) < perPage {
			return all, false, nil
		}
	}
}

// CountRecords returns the number of records matching filter.
func (c *Client) CountRecords(ctx context.Context, collection, filter string) (int, error) {
	result, err := c.ListRecords(ctx, collection, ListOptions{
		Filter:  filter,
		Fields:  "id",
		PerPage: 1,
	})
	if err != nil {
		return 0, err
	}
	return result.TotalItems, nil
}

// GetRecord returns a single record by ID.
func (c *Client) GetRecord(ctx context.Context, collection, id, expand string) (map[string]any, error) {
	query := url.Values{}
	if expand != "" {
		query.Set("expand", expand)
	}

	var record map[string]any
	path := "/api/collections/" + url.PathEscape(collection) + "/records/" + url.PathEscape(id)
	if err := c.getJSON(ctx, path, query, &record); err != nil {
		return nil, err
	}
	return record, nil
}

// CollectionField describes a field in a collection schema.
type CollectionField struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// Collection describes a PocketBase collection.
type Collection struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	System bool              `json:"system"`
	Fields []CollectionField `json:"fields"`
}

// ListCollections returns all collections with their field schemas.
func (c *Client) ListCollections(ctx context.Context) ([]Collection, error) {
	var result struct {
		Items []Collection `json:"items"`
	}

	query := url.Values{}
	query.Set("perPage", "200")
	query.Set("skipTotal", "true")

	if err := c.getJSON(ctx, "/api/collections", query, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		c.baseURL+"/api/health",
		nil,
	)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connect to PocketBase: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("PocketBase returned status: %d", resp.StatusCode)
	}

	var result struct {
		Code int `json:"code"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode health response: %w", err)
	}

	if result.Code != http.StatusOK {
		return fmt.Errorf("PocketBase health check failed: %d", result.Code)
	}

	return nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
