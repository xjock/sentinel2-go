package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Authenticator attaches credentials to an HTTP request.
type Authenticator interface {
	Apply(req *http.Request) error
}

// NoOpAuth does nothing; used for open STAC APIs such as Earth Search.
type NoOpAuth struct{}

func (NoOpAuth) Apply(req *http.Request) error { return nil }

// CDSEAuth implements CDSE Keycloak OAuth2 password grant flow.
type CDSEAuth struct {
	Username string
	Password string

	mu        sync.RWMutex
	token     string
	expiresAt time.Time
	margin    time.Duration
}

func NewCDSEAuth(username, password string) *CDSEAuth {
	return &CDSEAuth{
		Username: username,
		Password: password,
		margin:   30 * time.Second,
	}
}

func (o *CDSEAuth) Apply(req *http.Request) error {
	tok, err := o.tokenWithRefresh(req.Context())
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	return nil
}

func (o *CDSEAuth) tokenWithRefresh(ctx context.Context) (string, error) {
	o.mu.RLock()
	tok, valid := o.token, time.Now().Add(o.margin).Before(o.expiresAt)
	o.mu.RUnlock()
	if valid && tok != "" {
		return tok, nil
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	if time.Now().Add(o.margin).Before(o.expiresAt) && o.token != "" {
		return o.token, nil
	}
	return o.fetchToken(ctx)
}

func (o *CDSEAuth) fetchToken(ctx context.Context) (string, error) {
	data := url.Values{}
	data.Set("grant_type", "password")
	data.Set("client_id", "cdse-public")
	data.Set("username", o.Username)
	data.Set("password", o.Password)

	req, err := http.NewRequestWithContext(ctx, "POST", "https://identity.dataspace.copernicus.eu/auth/realms/CDSE/protocol/openid-connect/token", strings.NewReader(data.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", fmt.Errorf("decode token response: %w", err)
	}

	o.token = tr.AccessToken
	o.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return o.token, nil
}
