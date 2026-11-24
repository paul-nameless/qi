package agent

import (
	_ "embed"
	"strings"

	"github.com/paul-nameless/qi/internal/config"
	"github.com/paul-nameless/qi/internal/tools"
)

//go:embed builtin/qi-config/SKILL.md
var configSkill []byte

const (
	configSkillPath  = tools.EmbeddedPrefix + "skills/qi-config/SKILL.md"
	configSchemaPath = tools.EmbeddedPrefix + "skills/qi-config/schema.json"
)

func init() {
	tools.Embed(configSkillPath, configSkill)
	tools.Embed(configSchemaPath, config.Schema)
}

func BuiltinSkills() []Skill {
	meta, _ := ParseFrontmatter(string(configSkill))
	return []Skill{{Name: meta.Name, Description: strings.TrimSpace(meta.Description), Path: configSkillPath, Builtin: true}}
}
