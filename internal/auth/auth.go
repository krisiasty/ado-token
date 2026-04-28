package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	adoScope              = "499b84ac-1321-427f-aa17-267ca6975798/.default"
	aadBaseURL            = "https://login.microsoftonline.com" //nolint:gosec
	tokenRequestTimeout   = 30 * time.Second
	maxTokenResponseBytes = 1 << 20
)

var tokenHTTPClient = &http.Client{Timeout: tokenRequestTimeout}

type Token struct {
	AccessToken string
	ExpiresAt   time.Time
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	Error       string `json:"error"`
	ErrorDesc   string `json:"error_description"`
}

func FetchToken(ctx context.Context, tenantID, clientID, clientSecret string) (*Token, error) {
	endpoint, err := url.JoinPath(aadBaseURL, tenantID, "oauth2/v2.0/token")
	if err != nil {
		return nil, fmt.Errorf("building token endpoint: %w", err)
	}

	body := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {adoScope},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := tokenHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}
	if len(raw) == maxTokenResponseBytes {
		return nil, fmt.Errorf("token response exceeded %d bytes", maxTokenResponseBytes)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		var tr tokenResponse
		if json.Unmarshal(raw, &tr) == nil && tr.Error != "" {
			return nil, fmt.Errorf("AAD error %s (HTTP %d): %s", tr.Error, resp.StatusCode, tr.ErrorDesc)
		}
		return nil, fmt.Errorf("token endpoint returned HTTP %d", resp.StatusCode)
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token response contained no access_token")
	}
	if tr.ExpiresIn <= 0 {
		return nil, fmt.Errorf("token response contained invalid expires_in %d", tr.ExpiresIn)
	}

	return &Token{
		AccessToken: tr.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}
