package config

import "fmt"

const SelfChoice = "current"

type SubagentChoice struct {
	Name   string
	Model  string
	Effort string
	UseFor string
	Self   bool
}

func (s SubagentChoice) String() string {
	if s.Effort == "" {
		return fmt.Sprintf("%s (%s)", s.Name, s.Model)
	}
	return fmt.Sprintf("%s (%s, effort %s)", s.Name, s.Model, s.Effort)
}

func (c Config) SubagentChoices() []SubagentChoice {
	if len(c.subagents) > 0 {
		return c.subagents
	}
	return []SubagentChoice{{Name: SelfChoice, Model: c.Model, Effort: c.ReasoningEffort, UseFor: c.UseFor, Self: true}}
}

func (c Config) subagentChoices(names []string, self SubagentChoice, dir string, ownPrompt bool) ([]SubagentChoice, error) {
	if len(names) == 0 {
		return []SubagentChoice{self}, nil
	}
	choices := make([]SubagentChoice, 0, len(names))
	for i, name := range names {
		choice, err := c.subagentChoice(name, dir, ownPrompt)
		if err != nil {
			return nil, fmt.Errorf("subagent_profiles.%d %q: %w", i, name, err)
		}
		choices = append(choices, choice)
	}
	return choices, nil
}

func (c Config) subagentChoice(name, dir string, ownPrompt bool) (SubagentChoice, error) {
	if err := c.enterProfile(name); err != nil {
		return SubagentChoice{}, err
	}
	if ownPrompt {
		if err := c.loadSystemPrompt(dir); err != nil {
			return SubagentChoice{}, fmt.Errorf("key \"system_prompt_file\": %w", err)
		}
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return SubagentChoice{}, err
	}
	return SubagentChoice{Name: name, Model: c.Model, Effort: c.ReasoningEffort, UseFor: c.UseFor}, nil
}
