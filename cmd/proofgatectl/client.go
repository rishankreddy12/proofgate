package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Client struct {
	BaseURL     string
	Token       string
	Insecure    bool
	OutputJSON  bool
	HTTPClient  *http.Client
	warningOnce sync.Once
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
}


func NewClient(profileName, overrideServer string, overrideInsecure, outputJSON bool) (*Client, error) {
	pName, profile, err := getActiveProfile(profileName)
	if err != nil && profileName != "" {
		return nil, err
	}

	serverURL := profile.Server
	if overrideServer != "" {
		serverURL = overrideServer
	}
	if serverURL == "" {
		serverURL = "http://127.0.0.1:9090"
	}
	serverURL = strings.TrimRight(serverURL, "/")

	insecure := profile.Insecure || overrideInsecure

	var token string
	if sess, err := loadSession(pName); err == nil && sess != nil {
		token = sess.Token
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure, //nolint:gosec
		},
	}

	c := &Client{
		BaseURL:    serverURL,
		Token:      token,
		Insecure:   insecure,
		OutputJSON: outputJSON,
		HTTPClient: &http.Client{
			Transport: tr,
			Timeout:   30 * time.Second,
		},
	}
	return c, nil
}

func (c *Client) warnInsecure() {
	if c.Insecure {
		c.warningOnce.Do(func() {
			fmt.Fprintln(os.Stderr, "WARNING: TLS verification disabled. Session tokens may be intercepted.")
		})
	}
}

func (c *Client) Do(ctx context.Context, method, path string, body any, out any) (int, error) {
	c.warnInsecure()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("cannot connect to server at %s: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp ErrorResponse
		_ = json.Unmarshal(respBytes, &errResp)
		msg := errResp.Message
		if msg == "" {
			msg = string(respBytes)
		}

		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return resp.StatusCode, fmt.Errorf("not authenticated. Run 'proofgatectl login' first")
		case http.StatusForbidden:
			return resp.StatusCode, fmt.Errorf("permission denied (%s)", msg)
		case http.StatusNotFound:
			return resp.StatusCode, fmt.Errorf("not found: %s", msg)
		case http.StatusTooManyRequests:
			return resp.StatusCode, fmt.Errorf("rate limited: %s", msg)
		default:
			if resp.StatusCode >= 500 {
				return resp.StatusCode, fmt.Errorf("server error: %s", msg)
			}
			return resp.StatusCode, fmt.Errorf("%s", msg)
		}
	}

	if out != nil && len(respBytes) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return resp.StatusCode, fmt.Errorf("decode response: %w", err)
		}
	}

	return resp.StatusCode, nil
}

func (c *Client) Stream(ctx context.Context, method, path string, body any) (*http.Response, error) {
	c.warnInsecure()

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(b)
	}

	url := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream")

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	streamingClient := &http.Client{
		Transport: c.HTTPClient.Transport,
		Timeout:   0,
	}

	resp, err := streamingClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to server at %s: %w", c.BaseURL, err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBytes, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		_ = json.Unmarshal(respBytes, &errResp)
		msg := errResp.Message
		if msg == "" {
			msg = string(respBytes)
		}
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, fmt.Errorf("not authenticated. Run 'proofgatectl login' first")
		case http.StatusForbidden:
			return nil, fmt.Errorf("permission denied (%s)", msg)
		case http.StatusNotFound:
			return nil, fmt.Errorf("not found: %s", msg)
		default:
			return nil, fmt.Errorf("%s", msg)
		}
	}

	return resp, nil
}
