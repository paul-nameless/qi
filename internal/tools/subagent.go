package tools

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/paul-nameless/qi/internal/config"
)

type subagentArgs struct {
	Prompt  string `json:"prompt"`
	Profile string `json:"profile"`
	Effort  string `json:"effort"`
}

const SubagentTimeout = 30 * time.Minute

const subagentIntro = "run a task in a separate agent with a fresh context and get back only its final answer. Use it for self-contained side work (searching a codebase, reviewing, summarizing logs) to keep this conversation small, and run several at once for independent tasks. The subagent sees none of this conversation, so put every detail it needs in the prompt."

const selfUseFor = "this agent's own model and settings"

func subagentMenu(choices []config.SubagentChoice) string {
	var b strings.Builder
	for _, c := range choices {
		useFor := c.UseFor
		if useFor == "" && c.Self {
			useFor = selfUseFor
		}
		fmt.Fprintf(&b, "  - %s", c)
		if useFor != "" {
			fmt.Fprintf(&b, ": %s", useFor)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "  Efforts: %s. Higher effort thinks longer and costs more; not every model supports none, minimal, xhigh or max.", strings.Join(config.Efforts, ", "))
	return b.String()
}

func subagentDoc(choices []config.SubagentChoice) string {
	return "subagent(prompt, {profile, effort}) → string — " + subagentIntro + " It has the same functions and the current mode. At most " + strconv.Itoa(maxCellSubagents) + " per run call; the run timeout pauses while a subagent runs (each has its own " + strconv.Itoa(int(SubagentTimeout.Minutes())) + " minute limit). profile defaults to the first one below, effort to the profile's own.\n  Profiles:\n" + subagentMenu(choices)
}

func Subagent(choices []config.SubagentChoice) *Function {
	names := make([]string, len(choices))
	for i, c := range choices {
		names[i] = c.Name
	}
	return &Function{
		Name:      "subagent",
		HookNames: []string{"Agent", "Task"},
		Code: CodeSpec{
			Name:     "subagent",
			Params:   []string{"prompt"},
			Required: 1,
			Options:  []string{"profile", "effort"},
			Doc:      subagentDoc(choices),
		},
		Label: func(env *Env, in json.RawMessage) string {
			a, _ := decode[subagentArgs](in)
			return fmt.Sprintf("subagent %s/%s %s", cmp.Or(a.Profile, names[0]), cmp.Or(a.Effort, "default"), OneLine(a.Prompt, 50))
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
			i := 0
			if a.Profile != "" {
				if i = slices.Index(names, a.Profile); i < 0 {
					return errResult("unknown profile %q: use one of %s", a.Profile, strings.Join(names, ", "))
				}
			}
			if a.Effort != "" && !slices.Contains(config.Efforts, a.Effort) {
				return errResult("unknown effort %q: use one of %s", a.Effort, strings.Join(config.Efforts, ", "))
			}
			answer, err := env.Subagent(ctx, subagentTask(choices[i], a), progress)
			if err != nil {
				return errResult("subagent failed: %v", err)
			}
			summary := fmt.Sprintf("%d chars", len(answer))
			return Result{Text: answer, Summary: summary}
		},
	}
}

func subagentTask(c config.SubagentChoice, a subagentArgs) SubagentTask {
	task := SubagentTask{Prompt: a.Prompt, Effort: a.Effort}
	if c.Self {
		task.Model = c.Model
	} else {
		task.Profile = c.Name
	}
	return task
}
