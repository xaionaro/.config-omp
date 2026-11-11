package codingskill

import "strings"

// eciPresetPrefix starts every generated ECI preset agent name.
//
// Example: eci-preset-explorer-anthropic-default is an ECI preset agent.
const eciPresetPrefix = "eci-preset-"

// callTools lists the tool calls the policy checks; an ECI preset agent must finish its role reads before any of them.
//
// Example: an orchestrator's task call is checked before it spawns workers.
var callTools = []string{"write", "edit", "task"}

// presetRole names an ECI preset role, the agent name without prefix and camp/profile suffix.
//
// Example: eci-preset-critic-step2-openai-max has role critic-step2.
type presetRole string

// roleSkills maps each ECI preset role to the skills its template tells it to read, in denial order.
//
// The ECI router's Module routing table owns this mapping; the preset templates
// restate it and TestECITemplatesNameRoleSkills keeps both in sync.
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

// requiredRoleSkills returns the skills an agent must read before checked calls.
//
// Non-ECI agents need none. An unknown ECI role needs only the router, because
// the router says an unknown role must not deny or stall work.
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
