package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/paul-nameless/aicode/internal/config"
	"github.com/paul-nameless/aicode/internal/llm"
)

type subagentArgs struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

func Subagent(mainModel string, menu []config.SubagentModel, efforts []string) *Tool {
	var desc strings.Builder
	desc.WriteString("Run a task in a separate agent with a fresh context and get back only its final answer. Use it for self-contained side work (searching a codebase, reviewing, summarizing logs) to keep this conversation small, and run several at once for independent tasks. The subagent sees none of this conversation, so put every detail it needs in the prompt. It has the same tools and the current mode.\n\nModels:\n")
	names := []string{}
	if len(menu) == 0 {
		fmt.Fprintf(&desc, "- %s: the current model\n", mainModel)
		names = append(names, mainModel)
	}
	for _, m := range menu {
		fmt.Fprintf(&desc, "- %s (%s): %s\n", m.Name, m.Model, m.UseFor)
		names = append(names, m.Name)
	}
	fmt.Fprintf(&desc, "\nEfforts: %s. Higher effort thinks longer and costs more.", strings.Join(efforts, ", "))
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{"type": "string", "description": "Complete task description for the subagent"},
			"model":  map[string]any{"type": "string", "enum": names},
			"effort": map[string]any{"type": "string", "enum": efforts},
		},
		"required": []string{"prompt", "model", "effort"},
	}
	raw, _ := json.Marshal(schema)
	return &Tool{
		Def: llm.ToolDef{Name: "subagent", Description: desc.String(), Schema: raw},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[subagentArgs](in)
			return fmt.Sprintf("subagent %s/%s %s", a.Model, a.Effort, OneLine(a.Prompt, 50))
		},
		Run: func(ctx context.Context, env *Env, in json.RawMessage, progress func(string)) Result {
			a, err := decode[subagentArgs](in)
			if err != nil {
				return errResult("%v", err)
			}
			if strings.TrimSpace(a.Prompt) == "" {
				return errResult("prompt is required")
			}
			if env.Subagent == nil {
				return errResult("subagents are not available here")
			}
			model, provider := a.Model, ""
			for _, m := range menu {
				if m.Name == a.Model {
					model, provider = m.Model, m.Provider
				}
			}
			answer, err := env.Subagent(ctx, a.Prompt, model, provider, a.Effort, progress)
			if err != nil {
				return errResult("subagent failed: %v", err)
			}
			return Result{Text: env.capOutput(answer), Summary: fmt.Sprintf("%d chars", len(answer))}
		},
	}
}
