package app

import (
	"context"
	"encoding/json"
	"errors"
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
	pricesURL      = "https://models.dev/api.json"
	pricesMaxAge   = 24 * time.Hour
	pricesMaxBody  = 64 << 20
	pricesRetryGap = 10 * time.Minute
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

type price struct {
	modelCost
	provider string
}

func (b *priceBook) lookup(model string) (price, bool) {
	if b == nil {
		return price{}, false
	}
	if strings.Contains(model, "/") {
		c, ok := b.Providers["openrouter"][model]
		return price{c, "openrouter"}, ok
	}
	for _, p := range priceProviders[1:] {
		if c, ok := b.Providers[p][model]; ok {
			return price{c, p}, true
		}
	}
	return price{}, false
}

func (p price) estimate(u llm.Usage, longCache bool) float64 {
	in, out, read, write := p.Input, p.Output, p.CacheRead, p.CacheWrite
	size := -1
	for _, t := range p.Tiers {
		if t.Tier.Type == "context" && u.Input > t.Tier.Size && t.Tier.Size > size {
			size = t.Tier.Size
			in, out = t.Input, t.Output
			read, write = cmpPrice(t.CacheRead, p.CacheRead), cmpPrice(t.CacheWrite, p.CacheWrite)
		}
	}
	writePrice := orPrice(write, in)
	if longCache && p.provider == "anthropic" {
		writePrice = 2 * in
	}
	uncached := max(0, u.Input-u.CacheRead-u.CacheWrite)
	total := float64(uncached)*in + float64(u.CacheRead)*orPrice(read, in) + float64(u.CacheWrite)*writePrice + float64(u.Output)*out
	return total / 1e6
}

func cmpPrice(p, fallback *float64) *float64 {
	if p != nil {
		return p
	}
	return fallback
}

func orPrice(p *float64, fallback float64) float64 {
	if p == nil {
		return fallback
	}
	return *p
}

type priceSource struct {
	url      string
	cache    string
	now      func() time.Time
	client   *http.Client
	failedAt time.Time
}

func defaultPrices() *priceSource {
	return &priceSource{url: pricesURL, cache: filepath.Join(config.DataDir(), "prices.json"), now: time.Now, client: &http.Client{Timeout: time.Minute}}
}

func (s *priceSource) stale() bool {
	b, err := s.read()
	return err != nil || s.now().Sub(b.Fetched) >= pricesMaxAge
}

func (s *priceSource) willFetch() bool {
	return s.stale() && s.now().Sub(s.failedAt) >= pricesRetryGap
}

func (s *priceSource) load(ctx context.Context) (*priceBook, error) {
	cached, cacheErr := s.read()
	if cacheErr == nil && s.now().Sub(cached.Fetched) < pricesMaxAge {
		return cached, nil
	}
	if !s.willFetch() {
		if cacheErr == nil {
			return cached, nil
		}
		return nil, fmt.Errorf("prices from %s: the last fetch failed less than %v ago", s.url, pricesRetryGap)
	}
	fresh, err := s.fetch(ctx)
	if err != nil {
		s.failedAt = s.now()
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

func (s *priceSource) read() (*priceBook, error) {
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

func (s *priceSource) write(b *priceBook) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.cache)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".prices-*.json")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(f.Name(), 0o644)
	}
	if werr == nil {
		werr = os.Rename(f.Name(), s.cache)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}

func (s *priceSource) fetch(ctx context.Context) (*priceBook, error) {
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
	var all map[string]json.RawMessage
	if err := json.NewDecoder(io.LimitReader(resp.Body, pricesMaxBody)).Decode(&all); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	b := &priceBook{Fetched: s.now(), Providers: map[string]map[string]modelCost{}}
	n := 0
	for _, p := range priceProviders {
		var provider struct {
			Models map[string]json.RawMessage `json:"models"`
		}
		_ = json.Unmarshal(all[p], &provider)
		models := map[string]modelCost{}
		for id, raw := range provider.Models {
			var m struct {
				Cost *modelCost `json:"cost"`
			}
			if json.Unmarshal(raw, &m) == nil && m.Cost != nil {
				models[id] = *m.Cost
			}
		}
		b.Providers[p] = models
		n += len(models)
	}
	if n == 0 {
		return nil, errors.New("no prices for " + strings.Join(priceProviders, ", "))
	}
	return b, nil
}
