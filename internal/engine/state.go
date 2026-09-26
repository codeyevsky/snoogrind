package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func dataBase() string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return base
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share")
}

// DataDir resolves where state lives: $SNOOGRIND_HOME or <data home>/snoogrind.
func DataDir() string {
	if d := os.Getenv("SNOOGRIND_HOME"); d != "" {
		return d
	}
	return filepath.Join(dataBase(), "snoogrind")
}

func ProfilePath(browser string) string {
	p := filepath.Join(DataDir(), "profiles", browser)
	_ = os.MkdirAll(p, 0o755)
	return p
}

// Entry is one post we already handled · keyed by its t3_ id.
type Entry struct {
	At        string `json:"at"`
	Permalink string `json:"permalink,omitempty"`
	Sub       string `json:"sub,omitempty"`
	Title     string `json:"title,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Run struct {
	At      string `json:"at"`
	Feed    string `json:"feed"`
	Shared  int    `json:"shared"`
	Skipped int    `json:"skipped"`
	Scrolls int    `json:"scrolls"`
	Stopped string `json:"stopped"`
	DryRun  bool   `json:"dryRun"`
}

// ScrollRun records one doomscroll session · distance is the whole point, so
// it is kept across runs and totalled on the stats screen.
type ScrollRun struct {
	At      string `json:"at"`
	Feed    string `json:"feed"`
	Pixels  int64  `json:"pixels"`
	Seconds int    `json:"seconds"`
	Stopped string `json:"stopped"`
}

type State struct {
	User    string           `json:"user,omitempty"`
	Shared  map[string]Entry `json:"shared"`
	Failed  map[string]Entry `json:"failed"`
	Runs    []Run            `json:"runs"`
	Scrolls []ScrollRun      `json:"scrolls,omitempty"`
}

// ScrolledPixels is every doomscroll session added up.
func (s *State) ScrolledPixels() int64 {
	var n int64
	for _, r := range s.Scrolls {
		n += r.Pixels
	}
	return n
}

func statePath() string { return filepath.Join(DataDir(), "state.json") }

func LoadState() *State {
	s := &State{Shared: map[string]Entry{}, Failed: map[string]Entry{}}
	if b, err := os.ReadFile(statePath()); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Shared == nil {
		s.Shared = map[string]Entry{}
	}
	if s.Failed == nil {
		s.Failed = map[string]Entry{}
	}
	return s
}

func (s *State) Save() error {
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), append(b, '\n'), 0o644)
}

// SharedToday counts entries stamped with today's UTC date · reddit resets its
// daily achievement progress on the same clock, so this is the number to watch.
func (s *State) SharedToday() int {
	day := time.Now().UTC().Format("2006-01-02")
	n := 0
	for _, e := range s.Shared {
		if len(e.At) >= 10 && e.At[:10] == day {
			n++
		}
	}
	return n
}

// Recent returns the n most recently shared entries, newest last.
func (s *State) Recent(n int) []Entry {
	list := make([]Entry, 0, len(s.Shared))
	for _, e := range s.Shared {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].At < list[j].At })
	if len(list) > n {
		list = list[len(list)-n:]
	}
	return list
}
