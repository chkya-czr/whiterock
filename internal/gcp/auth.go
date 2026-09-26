// Package gcp provides credential-free (from configuration's perspective)
// access to Google APIs using the Cloud Run Job service account identity.
package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type TokenSource interface {
	Token(context.Context) (string, error)
}

// StaticTokenSource is used only by the explicit local development mode, with
// a short-lived user access token. Cloud Run uses MetadataTokenSource instead.
type StaticTokenSource struct{ AccessToken string }

func (s StaticTokenSource) Token(context.Context) (string, error) {
	if s.AccessToken == "" {
		return "", fmt.Errorf("local Google access token is empty")
	}
	return s.AccessToken, nil
}

type MetadataTokenSource struct{ HTTP *http.Client }

func (m MetadataTokenSource) Token(ctx context.Context) (string, error) {
	h := m.HTTP
	if h == nil {
		h = &http.Client{Timeout: 10 * time.Second}
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("Metadata-Flavor", "Google")
	r, e := h.Do(req)
	if e != nil {
		return "", fmt.Errorf("get Cloud Run service-account token: %w", e)
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("get Cloud Run service-account token: HTTP %s", r.Status)
	}
	var b struct {
		AccessToken string `json:"access_token"`
	}
	if e = json.NewDecoder(r.Body).Decode(&b); e != nil {
		return "", e
	}
	if b.AccessToken == "" {
		return "", fmt.Errorf("metadata token response was empty")
	}
	return b.AccessToken, nil
}
