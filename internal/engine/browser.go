// Package engine drives a real browser through Playwright so snoogrind can reuse
// the user's own Reddit login · no password, no API key. The launch flow is the
// one githubFlex and liout use, with a persistent profile per browser.
package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

const RedditHome = "https://www.reddit.com/"

// chromePaths lists where a Chrome family binary usually lives · real Chrome
// first, then Chromium/Brave/Edge, per platform.
func chromePaths() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		var out []string
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Chromium.app/Contents/MacOS/Chromium",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		} {
			out = append(out, filepath.Join("/Applications", app))
			if home != "" {
				out = append(out, filepath.Join(home, "Applications", app))
			}
		}
		return out
	case "windows":
		var out []string
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base == "" {
				continue
			}
			out = append(out,
				filepath.Join(base, `Google\Chrome\Application\chrome.exe`),
				filepath.Join(base, `Chromium\Application\chrome.exe`),
				filepath.Join(base, `BraveSoftware\Brave-Browser\Application\brave.exe`),
				filepath.Join(base, `Microsoft\Edge\Application\msedge.exe`),
			)
		}
		return out
	default:
		return []string{
			"/usr/bin/google-chrome-stable",
			"/usr/bin/google-chrome",
			"/opt/google/chrome/chrome",
			"/usr/bin/chromium",
			"/usr/bin/chromium-browser",
			"/usr/bin/brave",
			"/snap/bin/chromium",
		}
	}
}

var chromeNames = []string{
	"google-chrome-stable", "google-chrome", "chrome", "chromium", "chromium-browser",
	"brave", "brave-browser", "microsoft-edge", "msedge",
}

func chromeExe() string {
	for _, p := range chromePaths() {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	for _, n := range chromeNames {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// DefaultBrowser prefers real Chrome · its clipboard permissions can be granted
// up front, which is what makes the copy link check reliable.
func DefaultBrowser() string {
	if chromeExe() != "" {
		return "chrome"
	}
	return "firefox"
}

func Available() []string {
	var out []string
	if chromeExe() != "" {
		out = append(out, "chrome")
	}
	return append(out, "firefox")
}

type Session struct {
	pw   *playwright.Playwright
	ctx  playwright.BrowserContext
	Page playwright.Page
}

func (s *Session) Close() {
	if s == nil {
		return
	}
	if s.ctx != nil {
		_ = s.ctx.Close()
	}
	if s.pw != nil {
		_ = s.pw.Stop()
	}
}

// clearProfileLocks removes singleton locks a crashed run left behind, so the
// profile reopens without manual cleanup.
func clearProfileLocks(dir string) {
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket", "lockfile", ".parentlock"} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

// EnsureDriver installs the Playwright browsers if they're missing.
func EnsureDriver(browser string) error {
	run := &playwright.RunOptions{Verbose: false}
	if browser == "firefox" {
		run.Browsers = []string{"firefox"}
	} else {
		run.Browsers = []string{"chromium"}
	}
	return playwright.Install(run)
}

type Opts struct {
	Browser  string // chrome | firefox
	Headless bool
	// ProfileDir overrides snoogrind's own profile · point it at a browser
	// profile you already use and reddit skips the "prove your humanity"
	// wall it shows blank profiles. Close that browser first.
	ProfileDir string
}

// expandHome resolves a leading ~ so a config file can stay portable.
func expandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, strings.TrimPrefix(p, "~"))
}

var lockRe = regexp.MustCompile(`(?i)SingletonLock|ProcessSingleton|in use|already running|Failed to (create|launch)`)

// Open launches a persistent browser context · the Reddit login sticks between
// runs, so signing in once is enough.
func Open(o Opts) (*Session, error) {
	if o.Browser == "" {
		o.Browser = DefaultBrowser()
	}
	pw, err := playwright.Run()
	if err != nil {
		if EnsureDriver(o.Browser) == nil {
			pw, err = playwright.Run()
		}
		if err != nil {
			return nil, fmt.Errorf("playwright not ready (try: snoogrind install): %w", err)
		}
	}

	var engine playwright.BrowserType
	switch o.Browser {
	case "firefox":
		engine = pw.Firefox
	case "chrome", "chromium":
		engine = pw.Chromium
	default:
		_ = pw.Stop()
		return nil, fmt.Errorf("unknown browser %q (use chrome or firefox)", o.Browser)
	}

	opts := playwright.BrowserTypeLaunchPersistentContextOptions{
		Headless: playwright.Bool(o.Headless),
		Viewport: &playwright.Size{Width: 1400, Height: 950},
		Locale:   playwright.String("en-US"),
	}
	if o.Browser != "firefox" {
		opts.Args = []string{"--disable-blink-features=AutomationControlled"}
		opts.Channel = playwright.String("chrome")
		if exe := chromeExe(); exe != "" {
			opts.ExecutablePath = playwright.String(exe)
			opts.Channel = nil // an explicit path wins over the channel
		}
	}

	dir := ProfilePath(o.Browser)
	if strings.TrimSpace(o.ProfileDir) != "" {
		dir = expandHome(o.ProfileDir)
	}
	ctx, err := engine.LaunchPersistentContext(dir, opts)
	if err != nil && lockRe.MatchString(err.Error()) {
		clearProfileLocks(dir)
		ctx, err = engine.LaunchPersistentContext(dir, opts)
	}
	if err != nil {
		_ = pw.Stop()
		return nil, err
	}
	ctx.SetDefaultTimeout(45000)

	// Chromium can hand us the clipboard up front; that lets a run verify each
	// copy link by reading back the URL. Firefox refuses · we fall back to the
	// on page "copied" toast there.
	_ = ctx.GrantPermissions([]string{"clipboard-read", "clipboard-write"},
		playwright.BrowserContextGrantPermissionsOptions{Origin: playwright.String("https://www.reddit.com")})

	var page playwright.Page
	if pages := ctx.Pages(); len(pages) > 0 {
		page = pages[0]
	} else if page, err = ctx.NewPage(); err != nil {
		_ = ctx.Close()
		_ = pw.Stop()
		return nil, err
	}
	return &Session{pw: pw, ctx: ctx, Page: page}, nil
}

// Goto navigates and gives the feed's first batch a moment to render.
func (s *Session) Goto(url string) error {
	if _, err := s.Page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return err
	}
	s.Page.WaitForTimeout(1500)
	s.Dismiss()
	return nil
}

// whoJS asks reddit itself who we are. Scraping a /user/ link off the page
// picks up whoever wrote the first post instead, so this goes to the endpoint:
// it answers with the account name when signed in and with nothing when not.
const whoJS = `async () => {
  try {
    const r = await fetch('/api/me.json', { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    if (!r.ok) return "";
    const j = await r.json();
    return (j && j.data && j.data.name) || "";
  } catch (e) { return ""; }
}`

// Who returns the signed in username, or "" when logged out.
func (s *Session) Who() string {
	v, err := s.Page.Evaluate(whoJS)
	if err != nil {
		return ""
	}
	name, _ := v.(string)
	return strings.TrimSpace(name)
}

// LoggedIn reports whether this browser is carrying a real account · the
// anonymous session also gets cookies, so the username is the honest signal.
func (s *Session) LoggedIn() bool { return s.Who() != "" }

// SignIn opens Reddit and, if needed, waits for the user to log in by hand in
// the window we opened · progress is called with human readable status.
func (s *Session) SignIn(ctx context.Context, timeout time.Duration, progress func(string)) (string, error) {
	say := func(m string) {
		if progress != nil {
			progress(m)
		}
	}
	say("opening reddit…")
	if err := s.Goto(RedditHome); err != nil {
		return "", err
	}
	if s.LoggedIn() {
		who := s.Who()
		say("already signed in" + userSuffix(who))
		return who, nil
	}

	say("waiting for you to log in in the browser window…")
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		if s.LoggedIn() {
			who := s.Who()
			say("signed in" + userSuffix(who))
			return who, nil
		}
		s.Page.WaitForTimeout(1500)
	}
	return "", fmt.Errorf("timed out waiting for login")
}

func userSuffix(who string) string {
	if who == "" {
		return ""
	}
	return " · u/" + who
}

// dismissJS closes the banners reddit stacks over the feed (cookies, the
// "continue in app" bar, the login nag) so they can't swallow our clicks.
const dismissJS = `() => {
  const rx = /accept all|reject|got it|not now|maybe later|continue in browser|close/i;
  let n = 0;
  for (const el of document.querySelectorAll('button, [role="button"]')) {
    const label = (el.getAttribute('aria-label') || el.textContent || '').trim();
    if (!label || label.length > 40 || !rx.test(label)) continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    if (!el.closest('shreddit-async-loader, xpromo-app-selector, reddit-cookie-banner, shreddit-experience-tree, [class*="banner" i], [class*="bottom" i]')) continue;
    el.click(); n++;
  }
  document.querySelector('reddit-cookie-banner')?.remove();
  return n;
}`

// Dismiss clears overlay banners · best effort, never fatal.
func (s *Session) Dismiss() {
	_, _ = s.Page.Evaluate(dismissJS)
}

// pwCacheDir is where playwright unpacks the browsers it downloads.
func pwCacheDir() string {
	if p := os.Getenv("PLAYWRIGHT_BROWSERS_PATH"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Caches", "ms-playwright")
	case "windows":
		if la := os.Getenv("LocalAppData"); la != "" {
			return filepath.Join(la, "ms-playwright")
		}
		return filepath.Join(home, "AppData", "Local", "ms-playwright")
	default:
		if c := os.Getenv("XDG_CACHE_HOME"); c != "" {
			return filepath.Join(c, "ms-playwright")
		}
		return filepath.Join(home, ".cache", "ms-playwright")
	}
}

// NeedsDownload reports whether launching this browser will first pull a
// ~150 MB playwright build · a system Chrome we drive by path never does.
func NeedsDownload(browser string) bool {
	if browser != "firefox" && chromeExe() != "" {
		return false
	}
	prefix := "chromium"
	if browser == "firefox" {
		prefix = "firefox"
	}
	dir := pwCacheDir()
	if dir == "" {
		return true
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return true
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix+"-") {
			return false
		}
	}
	return true
}
