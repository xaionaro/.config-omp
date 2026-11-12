package codingskill

import "strings"

// eciPresetPrefix starts every generated ECI preset agent name.
//
// Example: eci-preset-explorer-anthropic-default is an ECI preset agent.
const eciPresetPrefix = "eci-preset-"

// presetRole names an ECI preset role, the agent name without prefix and camp/profile suffix.
//
// Example: eci-preset-critic-step2-openai-max has role critic-step2.
type presetRole string

// roleSkills maps each ECI preset role to the skills its template names, in denial order; it restates the router's Module routing table, and TestECITemplatesNameRoleSkills binds it to the templates.
//
// Example: roleSkills["explorer"] holds the router and the explore module.
var roleSkills = map[presetRole][]Skill{
	"explorer":        {skillECIRouter, skillECIExplore},
	"critic-step2":    {skillECIRouter, skillECICritique},
	"implementer":     {skillECIRouter, skillECIImplement},
	"fast-owner":      {skillECIRouter, skillECIFastPath},
	"reviewer":        {skillECIRouter, skillECIReview},
	"independent-e2e": {skillECIRouter, skillECIReview},
	"orchestrator":    {skillECIRouter, skillECIOrchestrator},
	"worker":          {skillECIRouter},
	"helper":          {skillECIRouter},
}

// requiredRoleSkills returns the skills an agent must read before checked calls: none for non-ECI agents, and only the router for an unknown ECI role, because the router says an unknown role must not deny or stall work.
//
// Example: eci-preset-orchestrator-openai-default needs the router and the task-orchestrator module.
func requiredRoleSkills(agentName string) []Skill {
	rest, ok := strings.CutPrefix(agentName, eciPresetPrefix)
	if !ok {
		return nil
	}
	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return []Skill{skillECIRouter}
	}
	if skills, ok := roleSkills[presetRole(strings.Join(parts[:len(parts)-2], "-"))]; ok {
		return skills
	}
	return []Skill{skillECIRouter}
}
