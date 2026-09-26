// Package llm uses a small OpenAI-compatible provider boundary. Its output is
// untrusted prose until validate accepts it.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Provider interface {
	Complete(context.Context, string, string) (string, error)
}
type DeepInfra struct {
	APIKey, Model, BaseURL string
	HTTP                   *http.Client
}

func NewDeepInfra(key, model string) *DeepInfra {
	if model == "" {
		model = "deepseek-ai/DeepSeek-V3.2"
	}
	return &DeepInfra{APIKey: key, Model: model, BaseURL: "https://api.deepinfra.com/v1/openai/chat/completions", HTTP: &http.Client{Timeout: 60 * time.Second}}
}
func (d *DeepInfra) Complete(ctx context.Context, system, user string) (string, error) {
	if d.APIKey == "" {
		return "", fmt.Errorf("DeepInfra API key is empty")
	}
	body := map[string]any{"model": d.Model, "temperature": 0, "response_format": map[string]string{"type": "json_object"}, "messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}}}
	raw, e := json.Marshal(body)
	if e != nil {
		return "", e
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, d.BaseURL, bytes.NewReader(raw))
	if e != nil {
		return "", e
	}
	req.Header.Set("Authorization", "Bearer "+d.APIKey)
	req.Header.Set("Content-Type", "application/json")
	r, e := d.HTTP.Do(req)
	if e != nil {
		return "", e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return "", fmt.Errorf("DeepInfra HTTP %s", r.Status)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if e = json.NewDecoder(r.Body).Decode(&out); e != nil {
		return "", e
	}
	if len(out.Choices) != 1 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", fmt.Errorf("DeepInfra returned no completion")
	}
	return out.Choices[0].Message.Content, nil
}
