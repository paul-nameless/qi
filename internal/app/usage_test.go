package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/session"
)

const testModelsDev = `{
  "openrouter": {"models": {"google/gemini-x": {"cost": {"input": 3, "output": 15}}}},
  "amazon-bedrock": {"models": {"openai.gpt-x": {"cost": {"input": 2, "output": 10, "cache_read": 0.1}}}},
  "anthropic": {"models": {
    "claude-x": {"cost": {"input": 4, "output": 20, "cache_read": 0.2, "cache_write": 5}},
    "claude-tiered": {"cost": {"input": 1, "output": 5, "cache_read": 0.1, "tiers": [{"input": 2, "output": 10, "cache_read": 0.2, "tier": {"type": "context", "size": 1000}}]}},
    "claude-free": {}
  }},
  "reseller": {"models": {"qwen-x": {"cost": {"input": 1, "output": 1}}}}
}`

func testBook(t *testing.T) *priceBook {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, testModelsDev) }))
	defer srv.Close()
	src := priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "prices.json"), now: time.Now, client: srv.Client()}
	b, err := src.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPriceLookupIsStrict(t *testing.T) {
	t.Parallel()
	b := testBook(t)
	for model, want := range map[string]bool{"google/gemini-x": true, "openai.gpt-x": true, "claude-x": true, "gemini-x": false, "qwen-x": false, "claude-free": false, "anthropic/claude-x": false} {
		if _, ok := b.lookup(model); ok != want {
			t.Errorf("%s: priced %v, want %v", model, ok, want)
		}
	}
}

func TestEstimate(t *testing.T) {
	t.Parallel()
	b := testBook(t)
	cases := []struct {
		model string
		u     llm.Usage
		want  float64
	}{
		{"claude-x", llm.Usage{Input: 1_000_000, CacheRead: 600_000, CacheWrite: 300_000, Output: 100_000}, 0.4 + 0.12 + 1.5 + 2},
		{"openai.gpt-x", llm.Usage{Input: 1_000_000, CacheRead: 500_000, CacheWrite: 100_000, Output: 0}, 0.8 + 0.05 + 0.2},
		{"claude-tiered", llm.Usage{Input: 1000, Output: 1000}, (1000*1 + 1000*5) / 1e6},
		{"claude-tiered", llm.Usage{Input: 1001, Output: 1000}, (1001*2 + 1000*10) / 1e6},
	}
	for _, c := range cases {
		cost, _ := b.lookup(c.model)
		if got := cost.estimate(c.u); fmt.Sprintf("%.9f", got) != fmt.Sprintf("%.9f", c.want) {
			t.Errorf("%s %+v: %v, want %v", c.model, c.u, got, c.want)
		}
	}
}

func TestPriceSourceCachesForADay(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	fail := atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, testModelsDev)
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	src := priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "sub", "prices.json"), now: func() time.Time { return now }, client: srv.Client()}
	if !src.stale() {
		t.Error("no cache yet but not stale")
	}
	if _, err := src.load(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("first load: %v, %d fetches", err, hits.Load())
	}
	now = now.Add(23 * time.Hour)
	if b, err := src.load(context.Background()); err != nil || hits.Load() != 1 || b == nil || src.stale() {
		t.Fatalf("within a day: %v, %d fetches", err, hits.Load())
	}
	now = now.Add(2 * time.Hour)
	fail.Store(true)
	b, err := src.load(context.Background())
	if err == nil || b == nil || hits.Load() != 2 || !strings.Contains(err.Error(), "not refreshed: HTTP 502") {
		t.Fatalf("stale cache on failure: %v, %v, %d fetches", b, err, hits.Load())
	}
	if _, ok := b.lookup("claude-x"); !ok {
		t.Error("stale cache lost its prices")
	}
	empty := priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "prices.json"), now: time.Now, client: srv.Client()}
	if b, err := empty.load(context.Background()); b != nil || err == nil {
		t.Errorf("no cache and a failed fetch: %v, %v", b, err)
	}
}

func writeUsageFile(t *testing.T, path, meta string, lines ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(append([]string{meta}, lines...), "\n")+"\n{\"type\":\"mess"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func usageEntry(at time.Time, model string, u llm.Usage) string {
	b, _ := json.Marshal(map[string]any{"type": "message", "time": at.UTC().Format(time.RFC3339Nano), "model": model, "usage": u})
	return string(b)
}

func TestUsageReport(t *testing.T) {
	t.Parallel()
	b := testBook(t)
	loc := time.FixedZone("UTC+5", 5*3600)
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, loc)
	root := t.TempDir()
	home, _ := os.UserHomeDir()
	alpha, beta := filepath.Join(home, "alpha"), "/srv/beta"
	reported := llm.Usage{Input: 1000, CacheRead: 500, Output: 10, Cost: new(0.5)}
	estimated := llm.Usage{Input: 1_000_000, Output: 0}
	unpriced := llm.Usage{Input: 100, Output: 10}
	main := filepath.Join(root, "alpha", "s1.jsonl")
	writeUsageFile(t, main, `{"type":"meta","cwd":"`+alpha+`","model":"google/gemini-x"}`,
		usageEntry(now.Add(-time.Hour), "", reported),
		usageEntry(now.Add(-2*time.Hour), "claude-x", estimated),
		usageEntry(now.Add(-4*time.Hour), "qwen-x", unpriced))
	writeUsageFile(t, filepath.Join(root, "alpha", "subagents", "c1.jsonl"), `{"type":"meta","cwd":"`+alpha+`","parent_session":"s1"}`,
		usageEntry(now.Add(-time.Hour), "claude-x", estimated))
	writeUsageFile(t, filepath.Join(root, "beta", "s2.jsonl"), `{"type":"meta","cwd":"`+beta+`"}`,
		usageEntry(now.AddDate(0, 0, -6), "google/gemini-x", reported),
		usageEntry(now.AddDate(0, 0, -29), "google/gemini-x", reported),
		usageEntry(now.AddDate(0, 0, -31), "google/gemini-x", reported))
	old := filepath.Join(root, "beta", "old.jsonl")
	writeUsageFile(t, old, `{"type":"meta","cwd":"`+beta+`"}`, usageEntry(now, "google/gemini-x", reported))
	if err := os.Chtimes(old, now.AddDate(0, 0, -40), now.AddDate(0, 0, -40)); err != nil {
		t.Fatal(err)
	}
	got, err := usageReport(usageOptions{root: root, session: &session.Session{Path: main, ID: "s1"}, now: now, book: b})
	if err != nil {
		t.Fatal(err)
	}
	want := `this session  ~$8.50 ($0.50 reported), 1 unpriced · 4 requests · input 2M (0% cached) · output 20
  subagents   ~$4.00 · 1 request (1)
today         ~$8.50 ($0.50 reported) · 3 requests
Tue 11-24     no price · 1 request
Mon 11-23     no requests
Sun 11-22     no requests
Sat 11-21     no requests
Fri 11-20     no requests
Thu 11-19     $0.50 · 1 request
last 30 days  ~$9.50 ($1.50 reported), 1 unpriced · 6 requests
by model, last 30 days
  claude-x         ~$8.00 · 2 requests · 0% cached
  google/gemini-x  $1.50 · 3 requests · 50% cached
  qwen-x           no price · 1 request · 0% cached
by project, last 30 days
  ~/alpha    ~$8.50 ($0.50 reported), 1 unpriced · 4 requests · 0% cached
  /srv/beta  $1.00 · 2 requests · 50% cached`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}
