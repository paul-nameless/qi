package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/llm"
)

type subagentArgs struct {
	Prompt string `json:"prompt"`
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

const SubagentTimeout = 30 * time.Minute

const subagentIntro = "run a task in a separate agent with a fresh context and get back only its final answer. Use it for self-contained side work (searching a codebase, reviewing, summarizing logs) to keep this conversation small, and run several at once for independent tasks. The subagent sees none of this conversation, so put every detail it needs in the prompt."

func subagentMenu(mainModel string, menu []config.SubagentModel, efforts []string, indent string) (string, []string) {
	var b strings.Builder
	names := []string{}
	if len(menu) == 0 {
		fmt.Fprintf(&b, "%s- %s: the current model\n", indent, mainModel)
		names = append(names, mainModel)
	}
	for _, m := range menu {
		fmt.Fprintf(&b, "%s- %s (%s): %s\n", indent, m.Name, m.Model, m.UseFor)
		names = append(names, m.Name)
	}
	if indent == "" {
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "%sEfforts: %s. Higher effort thinks longer and costs more.", indent, strings.Join(efforts, ", "))
	return b.String(), names
}

func subagentDoc(mainModel string, menu []config.SubagentModel, efforts []string) string {
	list, _ := subagentMenu(mainModel, menu, efforts, "  ")
	return "subagent(prompt, {model, effort}) → string — " + subagentIntro + " It has the same functions and the current mode. At most " + strconv.Itoa(maxCellSubagents) + " per run call; the run timeout pauses while a subagent runs (each has its own " + strconv.Itoa(int(SubagentTimeout.Minutes())) + " minute limit). model defaults to the first one below.\n  Models:\n" + list
}

func Subagent(mainModel string, menu []config.SubagentModel, efforts []string) *Tool {
	_, names := subagentMenu(mainModel, menu, efforts, "")
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"prompt": map[string]any{"type": "string"},
			"model":  map[string]any{"type": "string", "enum": names},
			"effort": map[string]any{"type": "string", "enum": efforts},
		},
		"required": []string{"prompt", "model", "effort"},
	}
	raw, _ := json.Marshal(schema)
	return &Tool{
		Def:          llm.ToolDef{Name: "subagent", Schema: raw},
		ParallelSafe: true,
		HookNames:    []string{"Agent", "Task"},
		Code: &CodeSpec{
			Name:   "subagent",
			Params: []string{"prompt"},
			Doc:    subagentDoc(mainModel, menu, efforts),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[subagentArgs](in)
			return fmt.Sprintf("subagent %s/%s %s", cmp.Or(a.Model, names[0]), cmp.Or(a.Effort, "default"), OneLine(a.Prompt, 50))
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
			model, provider := subagentModel(a.Model, mainModel, menu)
			answer, err := env.Subagent(ctx, SubagentTask{Prompt: a.Prompt, Model: model, Provider: provider, Effort: a.Effort}, progress)
			if err != nil {
				return errResult("subagent failed: %v", err)
			}
			summary := fmt.Sprintf("%d chars", len(answer))
			return Result{Text: answer, Summary: summary}
		},
	}
}

func subagentModel(name, mainModel string, menu []config.SubagentModel) (string, string) {
	if name == "" {
		if len(menu) == 0 {
			return mainModel, ""
		}
		return menu[0].Model, menu[0].Provider
	}
	model, provider := name, ""
	for _, m := range menu {
		if m.Name == name {
			model, provider = m.Model, m.Provider
		}
	}
	return model, provider
}
