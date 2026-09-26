// Package marketdata contains the Twelve Data boundary. Indicators are computed
// locally; endpoint-provided indicators are intentionally never trusted.
package marketdata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Candle struct {
	Date  time.Time
	Close float64
}
type Client interface {
	Daily(context.Context, string, int) ([]Candle, error)
}

type TwelveData struct {
	APIKey, BaseURL string
	HTTP            *http.Client
	limiter         chan time.Time
	once            sync.Once
}

// NewTwelveData permits no more than eight request starts per minute. The
// initial token makes the first request prompt without allowing a burst.
func NewTwelveData(key string) *TwelveData {
	return &TwelveData{APIKey: key, BaseURL: "https://api.twelvedata.com", HTTP: &http.Client{Timeout: 30 * time.Second}}
}
func (c *TwelveData) init() {
	c.once.Do(func() {
		c.limiter = make(chan time.Time, 1)
		c.limiter <- time.Now()
		go func() {
			t := time.NewTicker(7500 * time.Millisecond)
			defer t.Stop()
			for v := range t.C {
				c.limiter <- v
			}
		}()
	})
}
func (c *TwelveData) wait(ctx context.Context) error {
	c.init()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.limiter:
		return nil
	}
}
func (c *TwelveData) Daily(ctx context.Context, symbol string, outputSize int) ([]Candle, error) {
	if c.APIKey == "" {
		return nil, fmt.Errorf("Twelve Data API key is empty")
	}
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	u, _ := url.Parse(strings.TrimRight(c.BaseURL, "/") + "/time_series")
	q := u.Query()
	q.Set("symbol", symbol)
	q.Set("interval", "1day")
	q.Set("outputsize", strconv.Itoa(outputSize))
	q.Set("apikey", c.APIKey)
	q.Set("format", "JSON")
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	r, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Twelve Data %s: %w", symbol, err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Twelve Data %s: HTTP %s", symbol, r.Status)
	}
	var body struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
		Values  []struct {
			Datetime string `json:"datetime"`
			Close    string `json:"close"`
		} `json:"values"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode Twelve Data %s: %w", symbol, err)
	}
	if body.Message != "" {
		return nil, fmt.Errorf("Twelve Data %s: %s", symbol, body.Message)
	}
	if len(body.Values) == 0 {
		return nil, fmt.Errorf("Twelve Data %s: no daily values", symbol)
	}
	out := make([]Candle, 0, len(body.Values))
	for _, v := range body.Values {
		d, e := time.Parse("2006-01-02", v.Datetime)
		if e != nil {
			return nil, fmt.Errorf("parse Twelve Data date: %w", e)
		}
		close, e := strconv.ParseFloat(v.Close, 64)
		if e != nil {
			return nil, fmt.Errorf("parse Twelve Data close: %w", e)
		}
		out = append(out, Candle{d, close})
	}
	// API returns newest first; downstream math is chronological.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}
