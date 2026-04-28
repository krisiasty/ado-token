package auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	adoScope        = "499b84ac-1321-427f-aa17-267ca6975798/.default"
	tokenURLPattern = "https://login.microsoftonline.com/%s/oauth2/v2.0/token" //nolint:gosec
)

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

func FetchToken(tenantID, clientID, clientSecret string) (*Token, error) {
	endpoint := fmt.Sprintf(tokenURLPattern, url.PathEscape(tenantID))

	body := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"scope":         {adoScope},
	}

	resp, err := http.Post(endpoint, "application/x-www-form-urlencoded", strings.NewReader(body.Encode())) //nolint:noctx,gosec
	if err != nil {
		return nil, fmt.Errorf("token request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	var tr tokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}

	if tr.Error != "" {
		return nil, fmt.Errorf("AAD error %s: %s", tr.Error, tr.ErrorDesc)
	}
	if tr.AccessToken == "" {
		return nil, fmt.Errorf("token response contained no access_token (HTTP %d)", resp.StatusCode)
	}

	return &Token{
		AccessToken: tr.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}
