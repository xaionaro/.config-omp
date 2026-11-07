package main

import "encoding/json"

// laneRawUpdates preserves individual updates when a sibling has malformed fields.
//
// Example: Read a lane update beside an unrelated malformed update.
func laneRawUpdates(line []byte) ([]json.RawMessage, bool) {
	var envelope struct {
		Details struct {
			StatusUpdate struct {
				Updates json.RawMessage `json:"updates"`
			} `json:"status_update"`
		} `json:"details"`
	}
	if json.Unmarshal(line, &envelope) != nil {
		return nil, false
	}
	return rootRawCollection(envelope.Details.StatusUpdate.Updates)
}

// laneRawIdentifies reads only the selector fields from an update.
//
// Example: Identify a lane even if its evidence has the wrong type.
func laneRawIdentifies(
	raw json.RawMessage,
	lane syncUpdate,
) bool {
	var selector struct {
		Target              string `json:"target"`
		ID                  string `json:"id"`
		WorkRef             string `json:"work_ref"`
		RequiredEnvironment string `json:"required_environment"`
	}
	if json.Unmarshal(raw, &selector) != nil {
		return false
	}
	return selector.Target == "lane" && selector.ID == lane.ID && selector.WorkRef == lane.WorkRef && selector.RequiredEnvironment == lane.RequiredEnvironment.String()
}
