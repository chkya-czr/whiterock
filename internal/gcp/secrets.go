package gcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// SecretManager reads a named Secret Manager version. Secret material is never
// configured via an environment variable or written to logs.
type SecretManager struct {
	Project string
	Tokens  TokenSource
	HTTP    *http.Client
}

func (s SecretManager) Access(ctx context.Context, secret string) (string, error) {
	if s.Project == "" || secret == "" {
		return "", fmt.Errorf("project and secret name are required")
	}
	tok, e := s.Tokens.Token(ctx)
	if e != nil {
		return "", e
	}
	version := secret
	if !strings.Contains(secret, "/versions/") {
		version = "projects/" + s.Project + "/secrets/" + secret + "/versions/latest"
	}
	u := "https://secretmanager.googleapis.com/v1/" + version + ":access"
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	r, e := s.http().Do(req)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return "", fmt.Errorf("access secret %q: HTTP %s", secret, r.Status)
	}
	var b struct {
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}
	if e = json.NewDecoder(r.Body).Decode(&b); e != nil {
		return "", e
	}
	raw, e := base64.StdEncoding.DecodeString(b.Payload.Data)
	if e != nil {
		return "", fmt.Errorf("decode secret %q: %w", secret, e)
	}
	return string(raw), nil
}
func (s SecretManager) http() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return http.DefaultClient
}
