// Package news retrieves bounded, attributable public-news evidence.
package news

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Article struct {
	ID, Title, URL, Source, Date string
	Highlights                   []string
}
type Client struct {
	APIKey, BaseURL string
	HTTP            *http.Client
}

func NewExa(key string) Client {
	return Client{APIKey: key, BaseURL: "https://api.exa.ai/search", HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c Client) Search(ctx context.Context, query string, windowDays, numResults int, domains []string, now time.Time) ([]Article, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("Exa API key is empty")
	}
	if c.BaseURL == "" {
		c.BaseURL = "https://api.exa.ai/search"
	}
	if c.HTTP == nil {
		c.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"query": query, "category": "news", "numResults": numResults, "includeDomains": domains, "startPublishedDate": now.AddDate(0, 0, -windowDays).Format(time.RFC3339), "contents": map[string]any{"highlights": true}})
	req, e := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL, bytes.NewReader(body))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	r, e := c.HTTP.Do(req)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, fmt.Errorf("Exa HTTP %s", r.Status)
	}
	var out struct {
		Results []struct {
			Title         string   `json:"title"`
			URL           string   `json:"url"`
			PublishedDate string   `json:"publishedDate"`
			Highlights    []string `json:"highlights"`
		} `json:"results"`
	}
	if e = json.NewDecoder(r.Body).Decode(&out); e != nil {
		return nil, e
	}
	min := now.AddDate(0, 0, -windowDays-1)
	var articles []Article
	for _, x := range out.Results {
		d, e := time.Parse(time.RFC3339, x.PublishedDate)
		if e != nil || strings.TrimSpace(x.URL) == "" || d.Before(min) {
			continue
		}
		source := strings.TrimPrefix(strings.TrimPrefix(x.URL, "https://"), "http://")
		if i := strings.Index(source, "/"); i >= 0 {
			source = source[:i]
		}
		articles = append(articles, Article{ID: fmt.Sprintf("A%d", len(articles)+1), Title: x.Title, URL: x.URL, Source: source, Date: d.Format("2006-01-02"), Highlights: x.Highlights})
	}
	return articles, nil
}
