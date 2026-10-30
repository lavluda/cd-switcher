// Package sessionlog totals token use and estimated cost from the session
// logs Claude Code (~/.claude/projects/**/*.jsonl) and Codex
// ($CODEX_HOME/sessions/**/*.jsonl) write on this machine. Nothing is sent
// anywhere; the logs are only read.
//
// Files are read incrementally: a scan reads only the bytes appended since the
// last scan, so after the first pass a refresh is cheap.
package sessionlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lavluda/cd-switcher/internal/harness"
)

const (
	Claude = "claude"
	Codex  = "codex"
)

// Window is how far back logs are read.
const Window = 31 * 24 * time.Hour

// Usage is a token and cost total. Tokens are split so that Input is the
// uncached prompt only, for both tools.
type Usage struct {
	Requests   int
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
	// Unpriced counts requests whose model has no known price.
	Unpriced int
}

// Tokens is every token processed.
func (u Usage) Tokens() int64 { return u.Input + u.Output + u.CacheRead + u.CacheWrite }

// CacheHitRate is the share of prompt tokens served from cache.
func (u Usage) CacheHitRate() float64 {
	prompt := u.Input + u.CacheRead + u.CacheWrite
	if prompt == 0 {
		return 0
	}
	return float64(u.CacheRead) / float64(prompt)
}

func (u *Usage) add(o Usage) {
	u.Requests += o.Requests
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Cost += o.Cost
	u.Unpriced += o.Unpriced
}

type key struct {
	hour    int64 // unix hour
	tool    string
	model   string
	project string
}

type fileState struct {
	offset  int64
	buckets map[key]*Usage
	// Codex logs carry per-response records in newer versions and running
	// totals in older ones; records win once any are seen.
	fallback   map[key]*Usage
	hasRecords bool
	model, cwd string
	lastTotal  string
}

// Scanner holds what has been read so far. Safe for concurrent use.
type Scanner struct {
	mu    sync.Mutex
	files map[string]*fileState
	seen  map[string]bool // request ids already counted, across files
}

func NewScanner() *Scanner {
	return &Scanner{files: map[string]*fileState{}, seen: map[string]bool{}}
}

// Scan reads what is new in every log touched within Window.
func (s *Scanner) Scan() error {
	cutoff := time.Now().Add(-Window)
	roots := map[string]string{
		filepath.Join(harness.ClaudeDir(), "projects"): Claude,
		filepath.Join(harness.CodexHome(), "sessions"): Codex,
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for root, tool := range roots {
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
				return nil
			}
			fi, err := d.Info()
			if err != nil || fi.ModTime().Before(cutoff) {
				return nil
			}
			st := s.files[p]
			if st != nil && fi.Size() < st.offset {
				// Rewritten in place: start over so nothing is counted twice.
				*s = Scanner{files: map[string]*fileState{}, seen: map[string]bool{}}
				st = nil
			}
			if st == nil {
				st = &fileState{buckets: map[key]*Usage{}, fallback: map[key]*Usage{}}
				s.files[p] = st
			}
			if fi.Size() > st.offset {
				s.read(p, tool, st)
			}
			return nil
		})
	}
	return nil
}

// read parses complete lines from st.offset on.
func (s *Scanner) read(path, tool string, st *fileState) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(st.offset, io.SeekStart); err != nil {
		return
	}
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return // a partial last line is read again next time
		}
		st.offset += int64(len(line))
		if tool == Claude {
			s.claudeLine(line, st)
		} else {
			s.codexLine(line, st)
		}
	}
}

var (
	markUsage   = []byte(`"usage"`)
	markCodex   = []byte(`"token_count"`)
	markRecord  = []byte(`"token_usage_record"`)
	markContext = []byte(`"turn_context"`)
	markMeta    = []byte(`"session_meta"`)
)

func (s *Scanner) claudeLine(line []byte, st *fileState) {
	if !bytes.Contains(line, markUsage) {
		return
	}
	var d struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		RequestID string `json:"requestId"`
		Cwd       string `json:"cwd"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage struct {
				Input         int64  `json:"input_tokens"`
				Output        int64  `json:"output_tokens"`
				CacheRead     int64  `json:"cache_read_input_tokens"`
				CacheCreation int64  `json:"cache_creation_input_tokens"`
				Speed         string `json:"speed"`
				Breakdown     *struct {
					FiveMin int64 `json:"ephemeral_5m_input_tokens"`
					OneHour int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &d) != nil || d.Type != "assistant" || d.Message.Model == "" || d.Message.Model == "<synthetic>" {
		return
	}
	// One response is logged once per content block, and resumed sessions
	// copy earlier turns into the new file.
	id := d.Message.ID + "|" + d.RequestID
	if s.seen[id] {
		return
	}
	s.seen[id] = true
	t, err := time.Parse(time.RFC3339Nano, d.Timestamp)
	if err != nil {
		return
	}
	u := d.Message.Usage
	w5, w1 := u.CacheCreation, int64(0)
	if u.Breakdown != nil && u.Breakdown.FiveMin+u.Breakdown.OneHour > 0 {
		w5, w1 = u.Breakdown.FiveMin, u.Breakdown.OneHour
	}
	use := Usage{Requests: 1, Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: w5 + w1}
	if cost, ok := claudeCost(d.Message.Model, u.Input, u.Output, u.CacheRead, w5, w1, u.Speed == "fast"); ok {
		use.Cost = cost
	} else {
		use.Unpriced = 1
	}
	addTo(st.buckets, key{t.Unix() / 3600, Claude, d.Message.Model, project(d.Cwd)}, use)
}

type codexTokens struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (s *Scanner) codexLine(line []byte, st *fileState) {
	switch {
	case bytes.Contains(line, markContext), bytes.Contains(line, markMeta):
		var d struct {
			Payload struct {
				Model string `json:"model"`
				Cwd   string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &d) == nil {
			if d.Payload.Model != "" {
				st.model = d.Payload.Model
			}
			if d.Payload.Cwd != "" {
				st.cwd = d.Payload.Cwd
			}
		}
	case bytes.Contains(line, markRecord):
		var d struct {
			Timestamp string `json:"timestamp"`
			Payload   struct {
				ResponseID string      `json:"response_id"`
				Usage      codexTokens `json:"usage"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &d) != nil {
			return
		}
		st.hasRecords = true
		if id := d.Payload.ResponseID; id != "" {
			if s.seen[id] {
				return
			}
			s.seen[id] = true
		}
		s.addCodex(st.buckets, st, d.Timestamp, d.Payload.Usage)
	case bytes.Contains(line, markCodex):
		var d struct {
			Timestamp string `json:"timestamp"`
			Payload   struct {
				Info *struct {
					Total json.RawMessage `json:"total_token_usage"`
					Last  codexTokens     `json:"last_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &d) != nil || d.Payload.Info == nil {
			return
		}
		// Codex repeats the event when nothing changed.
		if total := string(d.Payload.Info.Total); total == st.lastTotal {
			return
		} else {
			st.lastTotal = total
		}
		s.addCodex(st.fallback, st, d.Timestamp, d.Payload.Info.Last)
	}
}

func (s *Scanner) addCodex(into map[key]*Usage, st *fileState, ts string, t codexTokens) {
	when, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return
	}
	model := st.model
	if model == "" {
		model = "codex"
	}
	if t.Input+t.Output == 0 {
		return // an event with nothing billed in it
	}
	// OpenAI counts cached and cache-write tokens inside input_tokens.
	uncached := max(t.Input-t.Cached-t.CacheWrite, 0)
	use := Usage{Requests: 1, Input: uncached, Output: t.Output, CacheRead: t.Cached, CacheWrite: t.CacheWrite}
	if cost, ok := codexCost(model, uncached, t.Output, t.Cached, t.CacheWrite); ok {
		use.Cost = cost
	} else {
		use.Unpriced = 1
	}
	addTo(into, key{when.Unix() / 3600, Codex, model, project(st.cwd)}, use)
}

func addTo(m map[key]*Usage, k key, u Usage) {
	if m[k] == nil {
		m[k] = &Usage{}
	}
	m[k].add(u)
}

// project names a working directory by its folder, with Claude worktrees
// counted under the repository they belong to.
func project(cwd string) string {
	if cwd == "" {
		return "(unknown)"
	}
	if i := strings.Index(cwd, "/.claude/worktrees/"); i > 0 {
		cwd = cwd[:i]
	}
	return filepath.Base(cwd)
}

// Row is one line of a breakdown table.
type Row struct {
	Name string
	Tool string // set when every request in the row came from one tool
	Usage
}

// Bucket is one bar of the chart: usage per tool in one hour or day.
type Bucket struct {
	Start time.Time
	Tools map[string]Usage
}

// Report is everything the Usage page shows for one range and tool filter.
type Report struct {
	Total     Usage
	Buckets   []Bucket
	Hourly    bool
	ByModel   []Row
	ByProject []Row
	Unpriced  []string // models with no known price
}

// Report totals usage since from (rounded down to the hour), for tool ("" for
// all). Today's view is hourly; longer ones are daily, in local time.
func (s *Scanner) Report(from time.Time, tool string, hourly bool) Report {
	s.mu.Lock()
	defer s.mu.Unlock()
	rep := Report{Hourly: hourly}
	step := 24 * time.Hour
	start := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.Local)
	if hourly {
		step, start = time.Hour, from
	}
	now := time.Now()
	for t := start; !t.After(now); t = nextStep(t, step) {
		rep.Buckets = append(rep.Buckets, Bucket{Start: t, Tools: map[string]Usage{}})
	}
	models, projects := map[string]*Row{}, map[string]*Row{}
	unpriced := map[string]bool{}
	fromHour := start.Unix() / 3600
	for _, st := range s.files {
		src := st.buckets
		if !st.hasRecords && len(st.fallback) > 0 {
			src = st.fallback
		}
		for k, u := range src {
			if k.hour < fromHour || (tool != "" && k.tool != tool) {
				continue
			}
			rep.Total.add(*u)
			at := time.Unix(k.hour*3600, 0)
			i := int(at.Sub(start) / step)
			if !hourly {
				i = daysBetween(start, at)
			}
			if i >= 0 && i < len(rep.Buckets) {
				b := rep.Buckets[i].Tools[k.tool]
				b.add(*u)
				rep.Buckets[i].Tools[k.tool] = b
			}
			addRow(models, k.model, k.tool, *u)
			addRow(projects, k.project, k.tool, *u)
			if u.Unpriced > 0 {
				unpriced[k.model] = true
			}
		}
	}
	rep.ByModel, rep.ByProject = sortedRows(models), sortedRows(projects)
	for m := range unpriced {
		rep.Unpriced = append(rep.Unpriced, m)
	}
	sort.Strings(rep.Unpriced)
	return rep
}

// nextStep adds a day by calendar so daylight-saving days stay aligned.
func nextStep(t time.Time, step time.Duration) time.Time {
	if step == time.Hour {
		return t.Add(time.Hour)
	}
	return t.AddDate(0, 0, 1)
}

func daysBetween(start, t time.Time) int {
	t = t.In(time.Local)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	n := 0
	for d := start; d.Before(day); d = d.AddDate(0, 0, 1) {
		n++
	}
	return n
}

func addRow(m map[string]*Row, name, tool string, u Usage) {
	r := m[name]
	if r == nil {
		r = &Row{Name: name, Tool: tool}
		m[name] = r
	} else if r.Tool != tool {
		r.Tool = ""
	}
	r.add(u)
}

// sortedRows orders by cost, then by tokens for rows without a price.
func sortedRows(m map[string]*Row) []Row {
	out := make([]Row, 0, len(m))
	for _, r := range m {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Cost != out[j].Cost {
			return out[i].Cost > out[j].Cost
		}
		return out[i].Tokens() > out[j].Tokens()
	})
	return out
}
