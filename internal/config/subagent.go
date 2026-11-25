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

func (c Config) subagentChoices(names []string, self SubagentChoice) ([]SubagentChoice, error) {
	if len(names) == 0 {
		return []SubagentChoice{self}, nil
	}
	choices := make([]SubagentChoice, 0, len(names))
	for _, name := range names {
		p := c
		if err := p.useProfile(name); err != nil {
			return nil, err
		}
		p.applyDefaults()
		choices = append(choices, SubagentChoice{Name: name, Model: p.Model, Effort: p.ReasoningEffort, UseFor: p.UseFor})
	}
	return choices, nil
}
