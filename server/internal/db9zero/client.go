// Package db9zero provides a client for the db9 API.
// Used to provision databases for mnemo tenants.
package db9zero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	defaultBaseURL = "https://db9.ai/api"
	defaultTimeout = 30 * time.Second
)

// Client is a db9 API client.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// Config holds db9 client configuration.
type Config struct {
	BaseURL string // API base URL (default: https://db9.ai/api)
	APIKey  string // Bearer token (required)
}

// NewClient creates a new db9 API client.
func NewClient(cfg Config) *Client {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		baseURL: baseURL,
		apiKey:  cfg.APIKey,
		httpClient: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// Database represents a db9 database instance.
type Database struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	State            string `json:"state"`
	Host             string `json:"host"`
	Port             int    `json:"port"`
	AdminUser        string `json:"admin_user"`
	AdminPassword    string `json:"admin_password"`
	ConnectionString string `json:"connection_string"`
}

// CreateDatabaseRequest is the request body for creating a database.
type CreateDatabaseRequest struct {
	Name string `json:"name"`
}

// CreateDatabase creates a new db9 database.
func (c *Client) CreateDatabase(ctx context.Context, name string) (*Database, error) {
	reqBody := CreateDatabaseRequest{Name: name}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/customer/databases", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("db9 API call: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("db9 API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var db Database
	if err := json.Unmarshal(respBody, &db); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &db, nil
}

// GetDatabase retrieves a database by ID.
func (c *Client) GetDatabase(ctx context.Context, id string) (*Database, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/customer/databases/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("db9 API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("db9 API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var db Database
	if err := json.Unmarshal(respBody, &db); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &db, nil
}

// DeleteDatabase deletes a database by ID.
func (c *Client) DeleteDatabase(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+"/customer/databases/"+id, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("db9 API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("db9 API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

// ExecuteSQL executes a SQL query on a database.
func (c *Client) ExecuteSQL(ctx context.Context, dbID, query string) error {
	reqBody := map[string]string{"query": query}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/customer/databases/"+dbID+"/sql", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("db9 API call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("db9 SQL returned %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
