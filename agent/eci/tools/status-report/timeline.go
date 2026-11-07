package main

import (
	"fmt"
	"slices"
	"strings"
)

// timelineEntry records one UTC half-hour and its independent path evidence.
//
// Example: Record both paths for one UTC half-hour.
type timelineEntry struct {
	Job         string `yaml:"job" json:"job"`
	ParentChain string `yaml:"parent_chain" json:"parent_chain"`
	Start       string `yaml:"start" json:"start"`
	End         string `yaml:"end" json:"end"`
	Fast        string `yaml:"fast" json:"fast"`
	Normal      string `yaml:"normal" json:"normal"`
	Evidence    string `yaml:"evidence" json:"evidence"`
	Uncertainty string `yaml:"uncertainty" json:"uncertainty"`
}

// writeTimeline renders each Job's supplied history in three columns.
//
// Example: A Job with Fast and normal events gets one table of its intervals.
func writeTimeline(
	b *strings.Builder,
	entries []timelineEntry,
) {
	if len(entries) == 0 {
		fmt.Fprintln(b, "Timeline history unavailable or no evidenced progress; consult source evidence.")
		return
	}
	groups := make(map[string][]timelineEntry)
	var jobs []string
	for _, entry := range entries {
		if _, ok := groups[entry.Job]; !ok {
			jobs = append(jobs, entry.Job)
		}
		groups[entry.Job] = append(groups[entry.Job], entry)
	}
	for _, job := range jobs {
		rows := groups[job]
		slices.SortFunc(rows, compareTimelineEntry)
		fmt.Fprintf(b, "Job %s (parent chain: %s)\n\n", timelineCell(job), timelineCell(rows[0].ParentChain))
		fmt.Fprintln(b, "| UTC interval | Fast | Normal |")
		fmt.Fprintln(b, "| --- | --- | --- |")
		for _, row := range rows {
			interval := row.Start + " – " + row.End
			fmt.Fprintf(b, "| %s | %s | %s |\n", timelineCell(interval), timelineCell(row.Fast), timelineCell(row.Normal))
		}
		fmt.Fprintln(b)
		writeTimelineEvidence(b, rows)
		fmt.Fprintln(b)
	}
}

// timelineCell renders a supplied value safely within a Markdown table cell.
//
// Example: A missing path history renders as unknown.
func timelineCell(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	cell := strings.ReplaceAll(value, "|", "\\|")
	return strings.ReplaceAll(cell, "\n", "<br>")
}

// compareTimelineEntry orders validated UTC intervals by their start times.
//
// Example: A 13:54 row precedes a 14:54 row regardless of input order.
func compareTimelineEntry(
	left timelineEntry,
	right timelineEntry,
) int {
	a, err := parseUTC(left.Start)
	if err != nil {
		return strings.Compare(left.Start, right.Start)
	}
	b, err := parseUTC(right.Start)
	if err != nil {
		return strings.Compare(left.Start, right.Start)
	}
	return a.Compare(b)
}

// writeTimelineEvidence retains evidence and uncertainty beside their interval.
//
// Example: An uncertain path attribution appears beneath its table.
func writeTimelineEvidence(
	b *strings.Builder,
	rows []timelineEntry,
) {
	for _, row := range rows {
		interval := row.Start + " – " + row.End
		if row.Evidence != "" {
			fmt.Fprintf(b, "- %s evidence: %s\n", timelineCell(interval), timelineCell(row.Evidence))
		}
		if row.Uncertainty != "" {
			fmt.Fprintf(b, "- %s uncertainty: %s\n", timelineCell(interval), timelineCell(row.Uncertainty))
		}
	}
}
