package session

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// tailBytes is how much of the end of a transcript is read. The state only
// depends on the last few entries; transcripts can grow to many MB.
const tailBytes = 512 * 1024

// maxLine caps a single JSONL line (tool results can be large).
const maxLine = 8 * 1024 * 1024

// entry holds only the transcript fields we use. Everything else, including
// all text content, is never decoded into memory we keep.
type entry struct {
	Type        string `json:"type"`
	IsSidechain bool   `json:"isSidechain"`
	IsMeta      bool   `json:"isMeta"`
	Cwd         string `json:"cwd"`
	GitBranch   string `json:"gitBranch"`
	Entrypoint  string `json:"entrypoint"`
	// Session title, depending on the Claude Code version: a /rename
	// ("custom-title"), a generated title ("ai-title") or a resume summary.
	CustomTitle string          `json:"customTitle"`
	AITitle     string          `json:"aiTitle"`
	Title       string          `json:"title"`
	Summary     string          `json:"summary"`
	Timestamp   time.Time       `json:"timestamp"`
	Message     json.RawMessage `json:"message"`
}

type message struct {
	StopReason *string         `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
}

type block struct {
	Type      string `json:"type"`
	ID        string `json:"id"`          // tool_use
	Name      string `json:"name"`        // tool_use
	ToolUseID string `json:"tool_use_id"` // tool_result
	Text      string `json:"text"`        // text: only inspected, never stored
}

// interruptMarker is what Claude Code writes when the user presses Esc.
const interruptMarker = "[Request interrupted"

// state is the running reduction over the transcript.
type state struct {
	status     Status
	pending    map[string]string // tool_use id -> tool name
	lastTool   string
	cwd        string
	branch     string
	entrypoint string
	title      string
	titleRank  int // custom > generated > summary
	last       time.Time
	// conv is the time of the last prompt or reply. Claude Code also writes
	// titles and summaries into old transcripts when a new session starts,
	// which changes the file's date without any activity.
	conv time.Time
}

func newState() *state {
	return &state{status: Idle, pending: map[string]string{}}
}

func (s *state) apply(e *entry) {
	if e.IsSidechain { // subagent traffic; the parent chain carries the status
		return
	}
	if e.Cwd != "" {
		s.cwd = e.Cwd
	}
	if e.GitBranch != "" {
		s.branch = e.GitBranch
	}
	if e.Entrypoint != "" {
		s.entrypoint = e.Entrypoint
	}
	if !e.Timestamp.IsZero() && e.Timestamp.After(s.last) {
		s.last = e.Timestamp
	}

	s.applyTitle(e)
	switch e.Type {
	case "assistant":
		s.applyAssistant(e)
	case "user":
		s.applyUser(e)
	}
	if (e.Type == "assistant" || e.Type == "user") && e.Timestamp.After(s.conv) {
		s.conv = e.Timestamp
	}
}

// applyTitle keeps the best session title seen: a user rename beats a
// generated title, which beats a resume summary. Later ones win at equal
// rank (titles get regenerated).
func (s *state) applyTitle(e *entry) {
	var t string
	rank := 0
	switch e.Type {
	case "custom-title":
		t, rank = firstNonEmpty(e.CustomTitle, e.Title), 3
	case "ai-title":
		t, rank = firstNonEmpty(e.AITitle, e.Title), 2
	case "summary":
		t, rank = e.Summary, 1
	default:
		return
	}
	t = strings.TrimSpace(strings.Join(strings.Fields(t), " "))
	if t != "" && rank >= s.titleRank {
		s.title, s.titleRank = t, rank
	}
}

func firstNonEmpty(v ...string) string {
	for _, x := range v {
		if x != "" {
			return x
		}
	}
	return ""
}

func (s *state) applyAssistant(e *entry) {
	var m message
	if json.Unmarshal(e.Message, &m) != nil {
		return
	}
	for _, b := range blocks(m.Content) {
		if b.Type == "tool_use" && b.ID != "" {
			s.pending[b.ID] = b.Name
			s.lastTool = b.Name
		}
	}
	if m.StopReason != nil && *m.StopReason == "end_turn" && len(s.pending) == 0 {
		s.status = Idle
		return
	}
	s.status = Working
}

func (s *state) applyUser(e *entry) {
	if e.IsMeta {
		return
	}
	var m message
	if json.Unmarshal(e.Message, &m) != nil {
		return
	}
	// Plain string content: a typed prompt.
	var str string
	if json.Unmarshal(m.Content, &str) == nil {
		s.onPrompt(strings.HasPrefix(str, interruptMarker))
		return
	}
	sawResult := false
	interrupted := false
	sawText := false
	for _, b := range blocks(m.Content) {
		switch b.Type {
		case "tool_result":
			delete(s.pending, b.ToolUseID)
			sawResult = true
		case "text":
			sawText = true
			if strings.HasPrefix(b.Text, interruptMarker) {
				interrupted = true
			}
		}
	}
	switch {
	case interrupted:
		s.onPrompt(true)
	case sawResult:
		s.status = Working // model resumes after a tool result
	case sawText:
		s.onPrompt(false)
	}
}

func (s *state) onPrompt(interrupted bool) {
	clear(s.pending)
	if interrupted {
		s.status = Idle
	} else {
		s.status = Working
	}
}

func blocks(raw json.RawMessage) []block {
	var bs []block
	if json.Unmarshal(raw, &bs) != nil {
		return nil
	}
	return bs
}

// Options control how raw transcript state becomes a displayed status.
type Options struct {
	Machine    string
	StaleAfter time.Duration // "working" with no file activity for this long -> Stale
	Now        time.Time
}

// ParseFile reads the end of one transcript and returns its status record.
func ParseFile(path string, opt Options) (Session, error) {
	return parseFile(path, opt, time.Time{})
}

// parseFile also takes the subagents' last write: while they work the
// transcript itself stays silent.
func parseFile(path string, opt Options, sub time.Time) (Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Session{}, err
	}
	st, err := parse(f, fi.Size())
	if err != nil {
		return Session{}, err
	}

	s := Session{
		ID:         strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Machine:    opt.Machine,
		Project:    projectName(st.cwd, path),
		Branch:     st.branch,
		Status:     st.status,
		Entrypoint: st.entrypoint,
		Title:      st.title,
		LastEvent:  st.last,
		Modified:   fi.ModTime(),
	}
	// Activity is the last prompt or reply, not the file's date (see conv);
	// the file's date only for transcripts without timestamps.
	if !st.conv.IsZero() && st.conv.Before(s.Modified) {
		s.Modified = st.conv
	}
	if sub.After(s.Modified) {
		s.Modified = sub
	}
	if len(st.pending) > 0 {
		s.Status = Working
		s.Tool = st.lastTool
	}
	if s.Status == Working && opt.StaleAfter > 0 && opt.Now.Sub(s.Modified) > opt.StaleAfter {
		s.Status = Stale
		s.Tool = ""
	}
	return s, nil
}

func parse(r io.ReaderAt, size int64) (*state, error) {
	off := size - tailBytes
	if off < 0 {
		off = 0
	}
	st := newState()
	// Summaries are written at the top of a resumed transcript, beyond the
	// tail of a long one: read the head for titles only.
	if off > 0 {
		head := newState()
		scanLines(io.NewSectionReader(r, 0, min(headBytes, off)), false, head)
		st.title, st.titleRank = head.title, head.titleRank
	}
	err := scanLines(io.NewSectionReader(r, off, size-off), off > 0, st)
	return st, err
}

// headBytes is how much of the start of a long transcript is read for its
// title.
const headBytes = 64 * 1024

func scanLines(r io.Reader, skipFirst bool, st *state) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), maxLine)
	first := skipFirst // first line may be cut in half
	for sc.Scan() {
		line := sc.Bytes()
		if first {
			first = false
			continue
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var e entry
		if json.Unmarshal(line, &e) != nil {
			continue // partial write or unknown shape
		}
		st.apply(&e)
	}
	if err := sc.Err(); err != nil && err != bufio.ErrTooLong {
		return err
	}
	return nil
}

// projectName returns the last path element of the working directory.
// Falls back to the transcript's parent folder if no cwd was seen.
func projectName(cwd, path string) string {
	if cwd != "" {
		// Normalise Windows separators too: the agent may parse a Windows
		// transcript on any OS in tests.
		cwd = strings.TrimRight(strings.ReplaceAll(cwd, `\`, "/"), "/")
		if i := strings.LastIndex(cwd, "/"); i >= 0 {
			return cwd[i+1:]
		}
		return cwd
	}
	return filepath.Base(filepath.Dir(path))
}
