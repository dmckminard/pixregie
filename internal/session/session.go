// Package session reads Claude Code session transcripts (~/.claude/projects)
// and reduces each one to a small, content-free status record.
//
// Privacy rule: nothing that leaves this package may contain prompt text,
// tool inputs, tool outputs or file contents. Only metadata (project name,
// branch, tool name, timestamps, status) is extracted.
//
// The transcript format is not a documented API and changes between Claude
// Code versions, so the parser is deliberately tolerant: unknown entry types
// and fields are ignored, and a line that fails to decode is skipped.
package session

import "time"

// Status is the coarse state shown on the display.
type Status string

const (
	// Working: the model is generating or a tool is running.
	Working Status = "working"
	// Waiting: Claude Code needs the user (permission prompt, question).
	// Only reliably known through the Notification hook; the transcript
	// alone cannot distinguish "tool running" from "tool awaiting approval".
	Waiting Status = "waiting"
	// Idle: the turn is over, nothing is happening.
	Idle Status = "idle"
	// Stale: the transcript says work was in progress, but the file has not
	// moved for a long time (crashed process, machine went away).
	Stale Status = "stale"
)

// Session is the content-free record shared with the collector.
type Session struct {
	ID         string `json:"id"`
	Machine    string `json:"machine"`
	Project    string `json:"project"`          // basename of the working directory
	Branch     string `json:"branch,omitempty"` // git branch, if any
	Status     Status `json:"status"`
	Tool       string `json:"tool,omitempty"` // tool currently running, e.g. "Bash"
	Entrypoint string `json:"entrypoint,omitempty"`
	// Title is the session's name as Claude Code shows it (rename,
	// generated title or summary). Unlike everything else here it is
	// derived from the conversation; it is only shown on the local board.
	Title     string    `json:"title,omitempty"`
	LastEvent time.Time `json:"last_event"` // timestamp of the last transcript entry
	Modified  time.Time `json:"modified"`   // file modification time
	// Open is true when Claude Code reported the session as running
	// (SessionStart hook seen, no SessionEnd yet).
	Open bool `json:"open,omitempty"`
}
