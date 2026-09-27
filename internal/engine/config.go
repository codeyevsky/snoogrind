package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Feeds are the ready made walk targets offered in the TUI.
var Feeds = map[string]string{
	"home":    "https://www.reddit.com/",
	"popular": "https://www.reddit.com/r/popular/",
	"all":     "https://www.reddit.com/r/all/",
	"best":    "https://www.reddit.com/best/",
}

// FeedOrder keeps the picker in a stable, sensible order.
var FeedOrder = []string{"home", "popular", "all", "best", "custom"}

// MaxSharesCap is the ceiling the UI will accept for one run. Reddit's daily
// achievement progress is nowhere near this, so it is a guard against a typo
// turning into an all night session, not a limit worth tuning.
const MaxSharesCap = 1000

type Config struct {
	Browser    string `json:"browser"`      // chrome | firefox
	Headless   bool   `json:"headless"`     // reddit's share menu needs a real window; keep false
	Feed       string `json:"feed"`         // one of Feeds, or "custom"
	CustomURL  string `json:"custom_url"`   // used when Feed == "custom"
	MaxShares  int    `json:"max_shares"`   // stop after this many copy links, 1..MaxSharesCap
	MaxScrolls int    `json:"max_scrolls"`  // stop after this many feed scrolls, 0 = no cap
	DelayMin   int    `json:"delay_min_ms"` // human like gap between posts
	DelayMax   int    `json:"delay_max_ms"`
	DryRun     bool   `json:"dry_run"`
	// ProfileDir is optional · an everyday browser profile to drive instead of
	// snoogrind's own, which is how you get past reddit's humanity challenge.
	ProfileDir string `json:"profile_dir,omitempty"`

	// The doomscroll screen · reddit's distance achievements only count how
	// far the feed has travelled, so that mode touches nothing else.
	ScrollFor      string `json:"scroll_for"`         // e.g. "2h"; "" runs until stopped
	ScrollStepMin  int    `json:"scroll_step_min_px"` // how far one flick moves
	ScrollStepMax  int    `json:"scroll_step_max_px"`
	ScrollDelayMin int    `json:"scroll_delay_min_ms"` // gap between flicks
	ScrollDelayMax int    `json:"scroll_delay_max_ms"`
}

func DefaultConfig() Config {
	return Config{
		Browser:  "chrome",
		Headless: false,
		Feed:     "home",
		// 50 is only a starting point · raise it to whatever you want up to
		// MaxSharesCap. Scrolling is uncapped by default, so the share count
		// is the thing that ends a run, not an arbitrary scroll budget.
		MaxShares:  50,
		MaxScrolls: 0,
		DelayMin:   1200,
		DelayMax:   2800,

		ScrollFor:      "1h",
		ScrollStepMin:  320,
		ScrollStepMax:  900,
		ScrollDelayMin: 250,
		ScrollDelayMax: 700,
	}
}

// ScrollDuration is how long the doomscroll screen should keep going · zero
// means it runs until you stop it.
func (c Config) ScrollDuration() time.Duration {
	s := strings.TrimSpace(c.ScrollFor)
	if s == "" || s == "0" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err != nil || d < 0 {
		return time.Hour
	}
	return d
}

// Clamp keeps the numbers inside what a run can actually honour.
func (c *Config) Clamp() {
	if c.MaxShares < 1 {
		c.MaxShares = 1
	}
	if c.MaxShares > MaxSharesCap {
		c.MaxShares = MaxSharesCap
	}
	if c.MaxScrolls < 0 {
		c.MaxScrolls = 0
	}
	if c.DelayMin < 0 {
		c.DelayMin = 0
	}
	if c.DelayMax < c.DelayMin {
		c.DelayMax = c.DelayMin
	}
	if c.ScrollStepMin < 40 {
		c.ScrollStepMin = 40
	}
	if c.ScrollStepMax < c.ScrollStepMin {
		c.ScrollStepMax = c.ScrollStepMin
	}
	if c.ScrollDelayMin < 50 {
		c.ScrollDelayMin = 50
	}
	if c.ScrollDelayMax < c.ScrollDelayMin {
		c.ScrollDelayMax = c.ScrollDelayMin
	}
}

// ScrollLabel describes the scroll budget for the status panel.
func (c Config) ScrollLabel() string {
	if c.MaxScrolls <= 0 {
		return "scroll till the feed runs dry"
	}
	return fmt.Sprintf("%d scrolls", c.MaxScrolls)
}

func ConfigPath() string { return filepath.Join(DataDir(), "config.json") }

func LoadConfig() Config {
	c := DefaultConfig()
	if b, err := os.ReadFile(ConfigPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if c.Browser == "" {
		c.Browser = DefaultBrowser()
	}
	c.Clamp()
	return c
}

func (c Config) Save() error {
	c.Clamp()
	if err := os.MkdirAll(DataDir(), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(ConfigPath(), append(b, '\n'), 0o644)
}

// URL is the page a run starts on.
func (c Config) URL() string {
	if c.Feed == "custom" {
		u := strings.TrimSpace(c.CustomURL)
		if u == "" {
			return Feeds["home"]
		}
		if !strings.HasPrefix(u, "http") {
			return "https://www.reddit.com/" + strings.TrimPrefix(u, "/")
		}
		return u
	}
	if u, ok := Feeds[c.Feed]; ok {
		return u
	}
	return Feeds["home"]
}

func (c Config) Min() time.Duration { return time.Duration(c.DelayMin) * time.Millisecond }
func (c Config) Max() time.Duration { return time.Duration(c.DelayMax) * time.Millisecond }
