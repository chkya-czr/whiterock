package news

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSearchSendsBoundedNewsRequestAndFiltersUnusableResults(t *testing.T) {
	now := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	c := Client{APIKey: "key", BaseURL: "https://exa.test/search", HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		for _, want := range []string{`"category":"news"`, `"numResults":5`, `"startPublishedDate":"2026-10-05T00:00:00Z"`, `"highlights":true`} {
			if !strings.Contains(text, want) {
				t.Errorf("request missing %s: %s", want, text)
			}
		}
		if r.Header.Get("Authorization") != "Bearer key" {
			t.Errorf("missing bearer auth")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"results":[
			{"title":"fresh","url":"https://reuters.com/a","publishedDate":"2026-10-10T00:00:00Z","highlights":["fact"]},
			{"title":"stale","url":"https://reuters.com/b","publishedDate":"2026-09-01T00:00:00Z","highlights":["old"]},
			{"title":"no date","url":"https://reuters.com/c","highlights":["bad"]}
		]}`)), Header: make(http.Header)}, nil
	})}}
	got, err := c.Search(context.Background(), "AMD", 7, 5, []string{"reuters.com"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "A1" || got[0].Source != "reuters.com" {
		t.Fatalf("got %#v", got)
	}
}

func TestSearchRejectsHTTPFailure(t *testing.T) {
	c := Client{APIKey: "key", BaseURL: "https://exa.test/search", HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Status: "500 Server Error", Body: io.NopCloser(strings.NewReader("no")), Header: make(http.Header)}, nil
	})}}
	if _, err := c.Search(context.Background(), "AMD", 7, 5, nil, time.Now()); err == nil {
		t.Fatal("wanted HTTP error")
	}
}
