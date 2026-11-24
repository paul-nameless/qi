package app

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/paul-nameless/qi/internal/agent"
	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
	"github.com/paul-nameless/qi/internal/mcp"
	"github.com/paul-nameless/qi/internal/tools"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata golden files")

const (
	anthropicReply = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	openAIReply    = "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"
	responsesReply = "data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"
)

func pinServer(t *testing.T, reply string, bodies *[][]byte) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		*bodies = append(*bodies, data)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func pinHistory(cfg config.Config) []llm.Message {
	model := cfg.Model
	think, _ := json.Marshal(map[string]any{"type": "thinking", "thinking": "plan", "signature": "SIG"})
	if cfg.API == "responses" {
		think, _ = json.Marshal(map[string]any{"type": "reasoning", "id": "rs_1", "encrypted_content": "ENC", "summary": []any{map[string]any{"type": "summary_text", "text": "plan"}}})
	}
	use := llm.Block{Type: llm.BlockToolUse, ID: "call_1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}
	result := llm.Block{Type: llm.BlockToolResult, ToolUseID: "call_1", Text: "a.go\nb.go", Images: []llm.Block{{Type: llm.BlockImage, MediaType: "image/png", Data: "AAAA", Width: 10, Height: 10}}}
	use.Name, use.Input = "run", json.RawMessage(`{"code":"return (await bash(\"ls\")).output"}`)
	result.Calls = []llm.InnerCall{{Tool: "bash", Label: "bash ls", OK: true, Summary: "2 lines", Ms: 3, Input: json.RawMessage(`{"command":"ls"}`)}}
	return []llm.Message{
		llm.UserText("list the files"),
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, Raw: think, Model: model},
			{Type: llm.BlockText, Text: "Listing."},
			use,
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{result}},
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "Two files."}}},
	}
}

func pinConfig(t *testing.T, dir, provider string) config.Config {
	path := filepath.Join(dir, "config.json")
	data := fmt.Sprintf(`{
  "providers": {
    "anth": {"api": "anthropic", "base_url": %[1]q, "api_key": "k"},
    "or": {"base_url": %[1]q, "api_key": "k"},
    "oa": {"base_url": %[2]q, "api_key": "k"},
    "rs": {"api": "responses", "base_url": %[2]q, "api_key": "k"}
  },
  "provider": %[3]q,
  "model": "claude-opus-5-5",
  "reasoning_effort": "high",
  "max_tokens": 32000,
  "cache_ttl": "1h",
  "instruction_files": [],
  "skill_dirs": [],
  "hook_files": [],
  "subagent_models": [
    {"name": "luna", "model": "openai/gpt-6-luna", "use_for": "cheap searches"},
    {"name": "opus", "model": "claude-opus-5-5", "use_for": "hard problems"}
  ],
  "mcp": {"notes": {"command": "notes-server", "description": "personal notes"}}
}`, "http://placeholder/openrouter.ai/api/v1", "http://placeholder/v1", provider)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Resolve(config.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func normalizeBody(t *testing.T, body []byte, cwd string) []byte {
	r := strings.NewReplacer(
		cwd, "<cwd>",
		"Platform: "+runtime.GOOS, "Platform: <os>",
		"Date: "+time.Now().Format("2006-01-02"), "Date: <date>",
	)
	var out bytes.Buffer
	if err := json.Indent(&out, []byte(r.Replace(string(body))), "", "  "); err != nil {
		t.Fatal(err)
	}
	return append(out.Bytes(), '\n')
}

func checkGolden(t *testing.T, name string, got []byte) {
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -run TestRequestBodiesPinned -update)", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("request body differs from %s; the request prefix must stay byte-stable:\n%s", path, firstDiff(string(want), string(got)))
	}
}

func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) && i < len(gl); i++ {
		if wl[i] != gl[i] {
			return fmt.Sprintf("line %d\nwant: %s\ngot:  %s", i+1, wl[i], gl[i])
		}
	}
	return fmt.Sprintf("want %d lines, got %d", len(wl), len(gl))
}

func TestRequestBodiesPinned(t *testing.T) {
	cases := []struct {
		provider, reply, golden string
	}{
		{"anth", anthropicReply, "request-anthropic.json"},
		{"or", openAIReply, "request-openrouter.json"},
		{"oa", openAIReply, "request-openai.json"},
		{"rs", responsesReply, "request-responses.json"},
	}
	for _, c := range cases {
		t.Run(c.golden, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			var bodies [][]byte
			srv := pinServer(t, c.reply, &bodies)
			dir := t.TempDir()
			cfg := pinConfig(t, dir, c.provider)
			cfg.BaseURL = strings.Replace(cfg.BaseURL, "http://placeholder", srv.URL, 1)
			cwd := t.TempDir()
			a := pinAgent(t, cfg, cwd)
			if _, _, err := a.Turn(context.Background(), []llm.Block{{Type: llm.BlockText, Text: "and now?"}}); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 1 {
				t.Fatalf("%d requests", len(bodies))
			}
			checkGolden(t, c.golden, normalizeBody(t, bodies[0], cwd))
		})
	}
}

func pinAgent(t *testing.T, cfg config.Config, cwd string) *agent.Agent {
	key, err := cfg.ResolveAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := buildAgent(cfg, options{}, cwd, newProvider(cfg, key), devNullPrinter(t), true, false, tools.MCPFuncs(mcp.NewManager(cfg.MCP))...)
	_, sys := agent.LoadPrompt(cwd, cfg, "")
	a.SetSystem(sys)
	setMessages(a, pinHistory(cfg))
	return a
}
