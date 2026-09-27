// snoogrind · walks your reddit feed and runs share → copy link on every
// post, which is what the share achievements count. Browser driven, so it uses
// the login you already have · no API key, no password.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/codeyevsky/snoogrind/internal/engine"
	"github.com/codeyevsky/snoogrind/internal/style"
)

// Version is the release shown in the panel; bump it with each tagged release.
const Version = "0.2.0"

type args struct{ opts map[string]string }

func parseArgs(list []string) (string, args) {
	a := args{opts: map[string]string{}}
	cmd := ""
	for _, v := range list {
		switch {
		case strings.HasPrefix(v, "--"):
			kv := strings.SplitN(strings.TrimPrefix(v, "--"), "=", 2)
			if len(kv) == 2 {
				a.opts[kv[0]] = kv[1]
			} else {
				a.opts[kv[0]] = "1"
			}
		case cmd == "":
			cmd = v
		}
	}
	return cmd, a
}

func (a args) str(key, def string) string {
	if v, ok := a.opts[key]; ok && v != "" && v != "1" {
		return v
	}
	return def
}

func (a args) num(key string, def int) int {
	if v, ok := a.opts[key]; ok {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (a args) has(key string) bool { _, ok := a.opts[key]; return ok }

const usage = `snoogrind · grinding reddit achievements

Run it bare for the interface · every screen and every option lives there.
The subcommands below are the same work without a terminal, for scripts.

  snoogrind                 open the TUI
  snoogrind login           sign in to reddit in a browser window
  snoogrind share           walk the feed and copy every post's link
  snoogrind scroll          just scroll, for the distance achievements
  snoogrind badges          work through the seven Getting Started badges
  snoogrind install         download the playwright browser build

run flags
  --feed=home|popular|all|best     which feed to walk   (default: saved config)
  --url=https://…                  walk any reddit page instead
  --max=50                         stop after this many shares, 1 to 1000
  --scrolls=0                      stop after this many scrolls, 0 = no cap
  --delay="1200 2800"              gap between posts, in ms
  --browser=chrome|firefox         which browser to drive
  --profile-dir=~/.config/…        drive a browser profile you already use
  --for=2h                         scroll only: how long to keep going, 0 = forever
  --dry-run                        list the posts without touching them
  --headless                       no visible window (reddit blocks it)

reddit shows a "prove your humanity" wall to blank browser profiles. Solve it
once in the window snoogrind opens and the saved profile keeps working ·
or point --profile-dir at your everyday browser profile, with it closed.
`

func main() {
	cmd, a := parseArgs(os.Args[1:])
	if a.has("help") || a.has("h") || cmd == "help" {
		fmt.Print(usage)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "":
		err = runTUI(ctx)
	case "login":
		err = cmdLogin(ctx, applyFlags(engine.LoadConfig(), a))
	case "share", "run":
		err = cmdRun(ctx, applyFlags(engine.LoadConfig(), a))
	case "scroll":
		err = cmdScroll(ctx, applyFlags(engine.LoadConfig(), a))
	case "badges", "start":
		err = cmdStart(ctx, applyFlags(engine.LoadConfig(), a), startOpts(a))
	case "install":
		err = cmdInstall(applyFlags(engine.LoadConfig(), a))
	default:
		fmt.Print(usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "  "+style.Tint(style.Red, "x "+err.Error()))
		os.Exit(1)
	}
}

// applyFlags folds command line overrides onto the saved config without
// persisting them · a one off `--max=5` should not rewrite the file.
func applyFlags(c engine.Config, a args) engine.Config {
	if v := a.str("feed", ""); v != "" {
		c.Feed = v
	}
	if v := a.str("url", ""); v != "" {
		c.Feed, c.CustomURL = "custom", v
	}
	if v := a.str("browser", ""); v != "" {
		c.Browser = v
	}
	if v := a.str("profile-dir", ""); v != "" {
		c.ProfileDir = v
	}
	c.MaxShares = a.num("max", c.MaxShares)
	c.MaxScrolls = a.num("scrolls", c.MaxScrolls)
	if v := a.str("delay", ""); v != "" {
		if lo, hi, ok := parseRange(v); ok {
			c.DelayMin, c.DelayMax = lo, hi
		}
	}
	if v := a.str("for", ""); v != "" {
		c.ScrollFor = v
	}
	if a.has("dry run") {
		c.DryRun = true
	}
	if a.has("headless") {
		c.Headless = true
	}
	c.Clamp()
	return c
}

// startOpts reads the Getting Started inputs that only you can supply.
func startOpts(a args) engine.StartOptions {
	o := engine.StartOptions{About: a.str("about", ""), Banner: a.str("banner", "")}
	if v := a.str("subs", ""); v != "" {
		for _, s := range strings.Split(v, ",") {
			if s = strings.TrimSpace(s); s != "" {
				o.Subs = append(o.Subs, s)
			}
		}
	}
	return o
}

// parseRange reads a two number range written any of the ways a person might
// type it: "300 900", "300,900" or "300-900".
func parseRange(s string) (int, int, bool) {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '-' || r == '\t'
	})
	if len(parts) == 0 {
		return 0, 0, false
	}
	lo, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	hi := lo
	if len(parts) > 1 {
		if v, err := strconv.Atoi(parts[1]); err == nil {
			hi = v
		}
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi, true
}

func openSession(c engine.Config) (*engine.Session, error) {
	return engine.Open(engine.Opts{Browser: c.Browser, Headless: c.Headless, ProfileDir: c.ProfileDir})
}

// ---------- work, shared by the CLI and the TUI ----------

// doLogin opens reddit and waits for a signed in session, reporting progress
// through say so either front end can show it.
func doLogin(ctx context.Context, c engine.Config, say func(string)) (string, error) {
	s, err := engine.Open(engine.Opts{Browser: c.Browser, Headless: false, ProfileDir: c.ProfileDir})
	if err != nil {
		return "", err
	}
	defer s.Close()

	who, err := s.SignIn(ctx, 15*time.Minute, say)
	if err != nil {
		return "", err
	}
	st := engine.LoadState()
	st.User = who
	_ = st.Save()
	return who, nil
}

// ---------- plain output commands ----------

func cmdInstall(c engine.Config) error {
	fmt.Println("  " + style.Tint(style.Dim, "fetching the playwright "+c.Browser+" build…"))
	if err := engine.EnsureDriver(c.Browser); err != nil {
		return err
	}
	fmt.Println("  " + style.Tint(style.Green, "[✓] ready"))
	return nil
}

func cmdLogin(ctx context.Context, c engine.Config) error {
	who, err := doLogin(ctx, c, func(msg string) { fmt.Println("  " + style.Tint(style.Dim, msg)) })
	if err != nil {
		return err
	}
	fmt.Println("  " + style.Tint(style.Green, "[✓] session saved · "+orNone(who)))
	return nil
}

func cmdRun(ctx context.Context, c engine.Config) error {
	if engine.NeedsDownload(c.Browser) {
		fmt.Println("  " + style.Tint(style.Yellow, "! first run · downloading the "+c.Browser+" build (~150 MB)"))
	}
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.Close()

	stats, err := s.Walk(ctx, engine.RunOptions{Cfg: c, On: printEvent})
	if stats != nil {
		fmt.Println()
		fmt.Println("  " + style.Tint(style.Orange, fmt.Sprintf(
			"shared %d · verified %d · already done %d · failed %d · scrolls %d",
			stats.Shared, stats.Verified, stats.Skipped, stats.Failed, stats.Scrolls)))
		fmt.Println("  " + style.Tint(style.Dim, "stopped: "+stats.Stopped))
	}
	return err
}

func cmdScroll(ctx context.Context, c engine.Config) error {
	s, err := openSession(c)
	if err != nil {
		return err
	}
	defer s.Close()

	last := ""
	st, err := s.Doomscroll(ctx, engine.ScrollOptions{
		Cfg: c,
		For: c.ScrollDuration(),
		On: func(st engine.ScrollStats, status string) {
			if status == last {
				return // the scrolling line repeats; only print what changed
			}
			last = status
			fmt.Printf("  %s  %s\n", style.Tint(style.Dim, st.Distance()), status)
		},
	})
	if st != nil {
		fmt.Println()
		fmt.Printf("  %s\n", style.Tint(style.Orange, fmt.Sprintf(
			"%s · %d flicks · %d posts in %s",
			st.Distance(), st.Flicks, st.Posts, st.Elapsed.Truncate(time.Second))))
		fmt.Println("  " + style.Tint(style.Dim, "stopped: "+st.Stopped))
	}
	return err
}

// printEvent is the plain stdout rendering of a walk, for headless of the TUI runs.
func printEvent(e engine.Event) {
	switch e.Kind {
	case engine.EvStatus:
		fmt.Println("  " + style.Tint(style.Dim, e.Text))
	case engine.EvSkipped:
		fmt.Println("   " + style.Tint(style.Dim, "· already shared  "+e.Post.Label()))
	case engine.EvFailed:
		fmt.Printf("   %s %s  · %s\n", style.Tint(style.Yellow, "[?]"), e.Post.Label(), e.Res.Err)
	case engine.EvDry:
		fmt.Printf("   %s %s\n", style.Tint(style.Cyan, "[~]"), e.Post.Label())
	case engine.EvShared:
		mark := "[+]"
		if e.Res.Verified {
			mark = "[✓]"
		}
		fmt.Printf("   %s %s\n", style.Tint(style.Green, mark), e.Post.Label())
	}
}

// label pads a field name to w columns and then dims it · padding a string
// that already carries SGR codes counts the escapes and never lines up.
func label(s string, w int) string {
	if n := len([]rune(s)); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return style.Tint(style.Dim, s)
}

// scrollForLabel names the doomscroll session length for the status panel.
func scrollForLabel(c engine.Config) string {
	if d := c.ScrollDuration(); d > 0 {
		return d.String()
	}
	return "until stopped"
}

func userSuffix(who string) string {
	if strings.TrimSpace(who) == "" {
		return ""
	}
	return " · u/" + who
}

// orNone names an empty field without leaning on punctuation.
func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "none"
}

// ---------- TUI ----------

func runTUI(ctx context.Context) error {
	// One raw mode reader for the session · every screen reads from it.
	if err := startKeys(); err != nil {
		return fmt.Errorf("no interactive terminal here · try `snoogrind --help` for the plain commands")
	}
	defer stopKeys()
	enterAlt()
	defer exitAlt()

	// login on top because nothing works before it, the three grinders in the
	// middle, and the two housekeeping tiles underneath
	layout := [][]menuItem{
		{{cmd: "login", desc: "sign in to reddit in a real browser window"}},
		{
			{cmd: "share", desc: "share → copy link on every post · for the share achievements"},
			{cmd: "scroll", desc: "scroll for hours · for the scroll distance achievements"},
			{cmd: "badges", title: "getting started", desc: "all seven getting started achievements"},
		},
		{
			{cmd: "settings", desc: "shares, pace, scroll session, browser"},
			{cmd: "leave", desc: "close snoogrind"},
		},
	}

	first := true
	for {
		cmd, ok := menuView(layout, panelRows, first)
		first = false
		if !ok {
			return nil
		}
		if cmd == "" { // d · flip the rehearsal switch and redraw
			cfg := engine.LoadConfig()
			cfg.DryRun = !cfg.DryRun
			if err := cfg.Save(); err != nil {
				message("error", "", "  "+style.Tint(style.Red, "x "+err.Error()))
			}
			continue
		}
		if cmd == "leave" {
			return nil
		}
		if err := screenFor(ctx, cmd); err != nil {
			message("error", "", "  "+style.Tint(style.Red, "x "+err.Error()))
		}
	}
}

// panelRows is the status block the menu shows under the wordmark.
func panelRows() []string {
	cfg := engine.LoadConfig()
	st := engine.LoadState()
	row := func(k, v string) string { return " " + label(k, 9) + " " + v }

	// limits mirrors the settings screen, so what you picked there is what you
	// read here · the four rows, in the same order.
	limits := fmt.Sprintf("%d shares · %d to %d ms apart · %s scroll · %s",
		cfg.MaxShares, cfg.DelayMin, cfg.DelayMax, scrollForLabel(cfg), cfg.Browser)
	if cfg.Headless {
		limits += " · headless"
	}
	if cfg.DryRun {
		limits += " · " + style.Tint(style.Cyan, "rehearsal")
	}
	return []string{
		row("account", orNone(st.User)),
		row("limits", limits),
	}
}

// screenFor runs one menu entry under its own cancellable context, so a stop
// inside a screen never takes the whole TUI down with it.
func screenFor(parent context.Context, cmd string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	cfg := engine.LoadConfig()
	switch cmd {
	case "share":
		return screenRun(ctx, cfg)
	case "scroll":
		return screenScroll(ctx, cfg)
	case "badges":
		runCfg, o, ok := startSetup(cfg)
		if !ok {
			return nil
		}
		return screenStart(ctx, runCfg, o)
	case "login":
		return screenLogin(ctx, cfg)
	case "settings":
		return screenSettings(cfg)
	}
	return nil
}

func screenLogin(ctx context.Context, cfg engine.Config) error {
	var who string
	err := busy("login", "sign in in the browser window · Ctrl+C to give up", func(say func(string)) error {
		var err error
		who, err = doLogin(ctx, cfg, say)
		return err
	})
	if err != nil {
		return err
	}
	message("login", "", "  "+style.Tint(style.Green, "[✓] session saved · u/"+orNone(who)))
	return nil
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n >= 0 {
		return n
	}
	return def
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

func boolIdx(b bool) int {
	if b {
		return 1
	}
	return 0
}
