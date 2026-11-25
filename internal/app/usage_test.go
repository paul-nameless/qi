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
    "claude-tiered": {"cost": {"input": 1, "output": 5, "cache_read": 0.1, "tiers": [{"input": 3, "output": 10, "tier": {"type": "context", "size": 5000}}, {"input": 2, "output": 10, "tier": {"type": "context", "size": 1000}}]}},
    "claude-broken": {"cost": {"input": "cheap"}},
    "claude-free": {}
  }},
  "reseller": {"models": {"qwen-x": {"cost": {"input": 1, "output": 1}}}},
  "weird": {"models": "not an object"}
}`

func testBook(t *testing.T) *priceBook {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, testModelsDev) }))
	defer srv.Close()
	src := &priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "prices.json"), now: time.Now, client: srv.Client()}
	b, err := src.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestPriceLookupIsStrict(t *testing.T) {
	t.Parallel()
	b := testBook(t)
	for model, want := range map[string]bool{"google/gemini-x": true, "openai.gpt-x": true, "claude-x": true, "gemini-x": false, "qwen-x": false, "claude-free": false, "claude-broken": false, "anthropic/claude-x": false} {
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
		long  bool
		want  float64
	}{
		{"claude-x", llm.Usage{Input: 1_000_000, CacheRead: 600_000, CacheWrite: 300_000, Output: 100_000}, false, 0.4 + 0.12 + 1.5 + 2},
		{"claude-x", llm.Usage{Input: 1_000_000, CacheRead: 600_000, CacheWrite: 300_000, Output: 100_000}, true, 0.4 + 0.12 + 2.4 + 2},
		{"openai.gpt-x", llm.Usage{Input: 1_000_000, CacheRead: 500_000, CacheWrite: 100_000}, true, 0.8 + 0.05 + 0.2},
		{"claude-tiered", llm.Usage{Input: 1000, Output: 1000}, false, (1000*1 + 1000*5) / 1e6},
		{"claude-tiered", llm.Usage{Input: 2000, CacheRead: 1000, Output: 1000}, false, (1000*2 + 1000*0.1 + 1000*10) / 1e6},
		{"claude-tiered", llm.Usage{Input: 6000, Output: 0}, false, 6000 * 3 / 1e6},
	}
	for _, c := range cases {
		p, _ := b.lookup(c.model)
		if got := p.estimate(c.u, c.long); fmt.Sprintf("%.9f", got) != fmt.Sprintf("%.9f", c.want) {
			t.Errorf("%s %+v long %v: %v, want %v", c.model, c.u, c.long, got, c.want)
		}
	}
}

func TestPriceSourceCachesForADay(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	var body atomic.Value
	body.Store(testModelsDev)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch b := body.Load().(string); b {
		case "down":
			http.Error(w, "down", http.StatusBadGateway)
		default:
			fmt.Fprint(w, b)
		}
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	src := &priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "sub", "prices.json"), now: func() time.Time { return now }, client: srv.Client()}
	if !src.willFetch() {
		t.Error("no cache yet but no fetch planned")
	}
	if _, err := src.load(context.Background()); err != nil || hits.Load() != 1 {
		t.Fatalf("first load: %v, %d fetches", err, hits.Load())
	}
	now = now.Add(23 * time.Hour)
	if b, err := src.load(context.Background()); err != nil || hits.Load() != 1 || b == nil || src.willFetch() {
		t.Fatalf("within a day: %v, %d fetches", err, hits.Load())
	}
	now = now.Add(2 * time.Hour)
	body.Store("down")
	b, err := src.load(context.Background())
	if err == nil || b == nil || hits.Load() != 2 || !strings.Contains(err.Error(), "not refreshed: HTTP 502") {
		t.Fatalf("stale cache on failure: %v, %v, %d fetches", b, err, hits.Load())
	}
	if _, ok := b.lookup("claude-x"); !ok {
		t.Error("stale cache lost its prices")
	}
	if b, err := src.load(context.Background()); err != nil || b == nil || hits.Load() != 2 {
		t.Errorf("a failed fetch is not retried within %v: %v, %d fetches", pricesRetryGap, err, hits.Load())
	}
	now = now.Add(pricesRetryGap)
	body.Store(`{"anthropic": {"models": {}}}`)
	if b, err := src.load(context.Background()); err == nil || b == nil || hits.Load() != 3 {
		t.Errorf("an empty price list must not replace the cache: %v, %d fetches", err, hits.Load())
	}
	if b, _ := src.read(); b == nil || len(b.Providers["anthropic"]) == 0 {
		t.Error("the cache was overwritten")
	}
	now = now.Add(pricesRetryGap)
	body.Store(testModelsDev)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := src.load(ctx); b == nil || err == nil || src.willFetch() {
		t.Errorf("a cancelled fetch keeps the stale prices and waits before trying again: %v, %v", b, err)
	}
	empty := &priceSource{url: srv.URL, cache: filepath.Join(t.TempDir(), "prices.json"), now: time.Now, client: srv.Client()}
	body.Store("down")
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

func usageAt(kind string, at time.Time, model string, u llm.Usage) string {
	b, _ := json.Marshal(map[string]any{"type": kind, "time": at.UTC().Format(time.RFC3339Nano), "model": model, "usage": u})
	return string(b)
}

func TestUsageReport(t *testing.T) {
	t.Parallel()
	b := testBook(t)
	loc := time.FixedZone("UTC+5", 5*3600)
	now := time.Date(2026, 11, 25, 3, 0, 0, 0, loc)
	real := t.TempDir()
	root := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, root); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	alpha, beta := filepath.Join(home, "alpha"), "/srv/beta"
	reported := llm.Usage{Input: 1000, CacheRead: 500, Output: 10, Cost: new(0.5)}
	main := filepath.Join(real, "alpha", "s1.jsonl")
	writeUsageFile(t, main, `{"type":"meta","kind":"interactive","cwd":"`+alpha+`","model":"google/gemini-x"}`,
		usageEntry(now.Add(-time.Hour), "", reported),
		usageAt("compaction", now.Add(-2*time.Hour), "claude-x", llm.Usage{Input: 1_000_000, CacheWrite: 500_000}),
		usageAt("usage", now.Add(-2*time.Hour), "claude-x", llm.Usage{}),
		usageEntry(now.Add(-4*time.Hour), "qwen-x", llm.Usage{Input: 100, Output: 10}))
	writeUsageFile(t, filepath.Join(real, "alpha", "subagents", "c1.jsonl"), `{"type":"meta","kind":"headless","cwd":"`+alpha+`","parent_session":"s1"}`,
		usageEntry(now.Add(-time.Hour), "claude-x", llm.Usage{Input: 1_000_000}))
	writeUsageFile(t, filepath.Join(real, "alpha", "subagents", "junk.jsonl"), "not json")
	writeUsageFile(t, filepath.Join(real, "beta", "s2.jsonl"), `{"type":"meta","kind":"headless","cwd":"`+beta+`"}`,
		usageEntry(now.AddDate(0, 0, -6), "google/gemini-x", reported),
		usageEntry(now.AddDate(0, 0, -29), "google/gemini-x", reported),
		usageEntry(now.AddDate(0, 0, -31), "google/gemini-x", reported))
	old := filepath.Join(real, "beta", "old.jsonl")
	writeUsageFile(t, old, `{"type":"meta","cwd":"`+beta+`"}`, usageEntry(now, "google/gemini-x", reported))
	if err := os.Chtimes(old, now.AddDate(0, 0, -40), now.AddDate(0, 0, -40)); err != nil {
		t.Fatal(err)
	}
	got, err := usageReport(context.Background(), usageOptions{root: root, session: &session.Session{Path: main, ID: "s1"}, now: now, book: b})
	if err != nil {
		t.Fatal(err)
	}
	want := `**this session** · **~$10.50** ($0.50 reported), 1 unpriced · 4 requests · input 2M (0% cached) · output 20 · cache write 500k
of which subagents (1) · **~$4.00** · 1 request · input 1M (0% cached) · output 0

| day | spend | requests |
|:--|--:|--:|
| **today** | ~$10.50 | 3 |
| Tue 11-24 | no price | 1 |
| Mon 11-23 | – | 0 |
| Sun 11-22 | – | 0 |
| Sat 11-21 | – | 0 |
| Fri 11-20 | – | 0 |
| Thu 11-19 | $0.50 | 1 |
| **last 30 days** | **~$11.50** | **6 (1 unpriced)** |

| model, last 30 days | spend | requests | cached |
|:--|--:|--:|--:|
| ` + "`claude-x`" + ` | ~$10.00 | 2 | 0% |
| ` + "`google/gemini-x`" + ` | $1.50 | 3 | 50% |
| ` + "`qwen-x`" + ` | no price | 1 | 0% |

| project, last 30 days | spend | requests | cached |
|:--|--:|--:|--:|
| ` + "`~/alpha`" + ` | ~$10.50 | 4 (1 unpriced) | 0% |
| ` + "`/srv/beta`" + ` | $1.00 | 2 | 50% |`
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := usageReport(ctx, usageOptions{root: root, now: now, book: b}); err == nil {
		t.Error("a cancelled report should stop")
	}
}

func TestCellName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a|b`c":                          "a/b'c",
		"x\x1b[31my\nz":                  "xy z",
		strings.Repeat("d/", 30) + "end": "…" + (strings.Repeat("d/", 30) + "end")[63-usageNameMax+1:],
	} {
		if got := cellName(in, usageNameMax); got != want {
			t.Errorf("cellName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaParentIgnoresBadFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{"empty.jsonl": "", "junk.jsonl": "not json\n", "msg.jsonl": `{"type":"message","parent_session":"p"}` + "\n"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := metaParent(path); got != "" {
			t.Errorf("%s: %q", name, got)
		}
	}
}
