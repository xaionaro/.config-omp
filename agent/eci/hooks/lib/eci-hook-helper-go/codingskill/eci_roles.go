package codingskill

import "strings"

const (
	// eciPresetPrefix starts every generated ECI preset agent name.
	//
	// Example: eci-preset-explorer-anthropic-default is an ECI preset agent.
	eciPresetPrefix = "eci-preset-"
	// presetSuffixParts counts the camp and profile parts that end every ECI preset agent name after its role.
	//
	// Example: eci-preset-critic-step2-openai-max ends with the parts openai and max.
	presetSuffixParts = 2
)

// AgentName is the omp name of the agent making a call; ECI preset agents are named eci-preset-<role>-<camp>-<profile>.
//
// Example: eci-preset-explorer-anthropic-default names an explorer preset agent.
type AgentName string

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

// requiredRoleSkills returns the skills the agent must read before checked calls: none for non-ECI agents, and only the router for an unknown ECI role, because the router says an unknown role must not deny or stall work.
//
// Example: eci-preset-orchestrator-openai-default needs the router and the task-orchestrator module.
func (name AgentName) requiredRoleSkills() []Skill {
	rest, ok := strings.CutPrefix(string(name), eciPresetPrefix)
	if !ok {
		return nil
	}
	parts := strings.Split(rest, "-")
	if len(parts) <= presetSuffixParts {
		return []Skill{skillECIRouter}
	}
	if skills, ok := roleSkills[presetRole(strings.Join(parts[:len(parts)-presetSuffixParts], "-"))]; ok {
		return skills
	}
	return []Skill{skillECIRouter}
}
