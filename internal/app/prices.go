package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
)

const (
	pricesURL     = "https://models.dev/api.json"
	pricesMaxAge  = 24 * time.Hour
	pricesMaxBody = 64 << 20
)

var priceProviders = []string{"openrouter", "amazon-bedrock", "anthropic", "openai", "google"}

type modelCost struct {
	Input      float64    `json:"input"`
	Output     float64    `json:"output"`
	CacheRead  *float64   `json:"cache_read,omitempty"`
	CacheWrite *float64   `json:"cache_write,omitempty"`
	Tiers      []costTier `json:"tiers,omitempty"`
}

type costTier struct {
	Input      float64  `json:"input"`
	Output     float64  `json:"output"`
	CacheRead  *float64 `json:"cache_read,omitempty"`
	CacheWrite *float64 `json:"cache_write,omitempty"`
	Tier       struct {
		Type string `json:"type"`
		Size int    `json:"size"`
	} `json:"tier"`
}

type priceBook struct {
	Fetched   time.Time                       `json:"fetched"`
	Providers map[string]map[string]modelCost `json:"providers"`
}

func (b *priceBook) lookup(model string) (modelCost, bool) {
	if b == nil {
		return modelCost{}, false
	}
	if strings.Contains(model, "/") {
		c, ok := b.Providers["openrouter"][model]
		return c, ok
	}
	for _, p := range priceProviders[1:] {
		if c, ok := b.Providers[p][model]; ok {
			return c, true
		}
	}
	return modelCost{}, false
}

func (c modelCost) estimate(u llm.Usage) float64 {
	in, out, read, write := c.Input, c.Output, c.CacheRead, c.CacheWrite
	for _, t := range c.Tiers {
		if t.Tier.Type == "context" && u.Input > t.Tier.Size {
			in, out, read, write = t.Input, t.Output, t.CacheRead, t.CacheWrite
		}
	}
	uncached := max(0, u.Input-u.CacheRead-u.CacheWrite)
	total := float64(uncached)*in + float64(u.CacheRead)*orPrice(read, in) + float64(u.CacheWrite)*orPrice(write, in) + float64(u.Output)*out
	return total / 1e6
}

func orPrice(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}

type priceSource struct {
	url    string
	cache  string
	now    func() time.Time
	client *http.Client
}

func defaultPrices() priceSource {
	return priceSource{url: pricesURL, cache: filepath.Join(config.DataDir(), "prices.json"), now: time.Now, client: &http.Client{Timeout: 30 * time.Second}}
}

func (s priceSource) stale() bool {
	b, err := s.read()
	return err != nil || s.now().Sub(b.Fetched) >= pricesMaxAge
}

func (s priceSource) load(ctx context.Context) (*priceBook, error) {
	cached, cacheErr := s.read()
	if cacheErr == nil && s.now().Sub(cached.Fetched) < pricesMaxAge {
		return cached, nil
	}
	fresh, err := s.fetch(ctx)
	if err != nil {
		if cacheErr == nil {
			return cached, fmt.Errorf("prices from %s not refreshed: %w", s.url, err)
		}
		return nil, fmt.Errorf("prices from %s: %w", s.url, err)
	}
	if err := s.write(fresh); err != nil {
		return fresh, fmt.Errorf("prices not cached: %w", err)
	}
	return fresh, nil
}

func (s priceSource) read() (*priceBook, error) {
	data, err := os.ReadFile(s.cache)
	if err != nil {
		return nil, err
	}
	var b priceBook
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s priceSource) write(b *priceBook) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.cache), 0o755); err != nil {
		return err
	}
	tmp := s.cache + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.cache)
}

func (s priceSource) fetch(ctx context.Context) (*priceBook, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "qi")
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var all map[string]struct {
		Models map[string]struct {
			Cost *modelCost `json:"cost"`
		} `json:"models"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, pricesMaxBody)).Decode(&all); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	b := &priceBook{Fetched: s.now(), Providers: map[string]map[string]modelCost{}}
	for _, p := range priceProviders {
		models := map[string]modelCost{}
		for id, m := range all[p].Models {
			if m.Cost != nil {
				models[id] = *m.Cost
			}
		}
		b.Providers[p] = models
	}
	return b, nil
}
