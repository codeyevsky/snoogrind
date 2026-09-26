package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// StepStatus is where one Getting Started step ended up.
type StepStatus int

const (
	StepPending StepStatus = iota
	StepRunning
	StepDone
	StepAlready // already satisfied before we got here
	StepManual  // only you can finish it · needs your content or a code
	StepFailed
)

type StepEvent struct {
	Index  int
	Badge  string // the achievement this step earns
	Need   string // what reddit asks for
	Status StepStatus
	Note   string
}

// StartOptions configures the Getting Started run.
type StartOptions struct {
	Cfg      Config
	Subs     []string // subreddits to join · five are needed for the full pair
	Searches []string // terms to search, one click each
	About    string   // profile description, if you want it set for you
	Banner   string   // path to a profile banner image
	Ctl      *Control
	On       func(StepEvent)
}

// DefaultSearches are throwaway terms · the badge counts result clicks, not
// what you searched for, and these land on big obvious subreddits.
var DefaultSearches = []string{
	"programming", "photography", "cooking", "space", "history",
	"music", "books", "science", "travel", "gardening",
}

// DefaultSubs are the communities a run joins when you name none. Six, so the
// five-subreddit badge still lands if one of them refuses.
var DefaultSubs = []string{
	"todayilearned", "mildlyinteresting", "AskReddit", "pics", "science", "books",
}

// GettingStarted works through the seven-badge newcomer ladder. Two of them
// cannot be automated — a profile needs your own words and a picture, and
// verification needs a code only you receive — so those report as manual
// unless you hand the content over.
func (s *Session) GettingStarted(ctx context.Context, o StartOptions) error {
	cfg := o.Cfg
	i := 0
	emit := func(badge, need string, st StepStatus, note string) {
		if o.On != nil {
			o.On(StepEvent{Index: i, Badge: badge, Need: need, Status: st, Note: note})
		}
	}
	halted := func() bool {
		select {
		case <-ctx.Done():
			return true
		default:
		}
		return o.Ctl.Stopped()
	}
	step := func(badge, need string, run func() (StepStatus, string)) {
		if halted() {
			return
		}
		emit(badge, need, StepRunning, "")
		st, note := run()
		emit(badge, need, st, note)
		i++
	}

	if err := s.Goto(RedditHome); err != nil {
		return err
	}
	if n := s.WaitForFeed(90*time.Second, nil); n == 0 {
		return fmt.Errorf("no feed rendered · reddit is still gating this browser profile")
	}
	who := s.Who()
	if who == "" {
		return fmt.Errorf("not signed in · run the login screen first")
	}

	step("Joined Reddit", "have a reddit account", func() (StepStatus, string) {
		return StepAlready, "u/" + who
	})

	step("Newcomer + Person of Interests", "join 1, then 5 subreddits", func() (StepStatus, string) {
		subs := o.Subs
		if len(subs) == 0 {
			subs = DefaultSubs
		}
		joined, already := 0, 0
		for _, sub := range subs {
			if halted() || joined+already >= 5 {
				break
			}
			switch s.joinSub(sub, cfg.DryRun) {
			case joinedNow:
				joined++
			case joinedBefore:
				already++
			}
			time.Sleep(jitter(cfg.Min(), cfg.Max()))
		}
		note := fmt.Sprintf("%d joined, %d already a member", joined, already)
		if joined+already >= 5 {
			return StepDone, note
		}
		if joined+already > 0 {
			return StepDone, note + " · under 5, run it again with more subs"
		}
		return StepFailed, "no join button matched on any subreddit"
	})

	step("Detective Doggo", "click 10 search results", func() (StepStatus, string) {
		terms := o.Searches
		if len(terms) == 0 {
			terms = DefaultSearches
		}
		clicks := 0
		for _, t := range terms {
			if halted() {
				break
			}
			if s.clickSearchResult(t, cfg.DryRun) {
				clicks++
			}
			time.Sleep(jitter(cfg.Min(), cfg.Max()))
		}
		if clicks == 0 {
			return StepFailed, "no clickable search results found"
		}
		return StepDone, fmt.Sprintf("%d of %d results clicked", clicks, len(terms))
	})

	step("Feed Finder", "change the feed type on home", func() (StepStatus, string) {
		n := s.cycleFeedType(cfg.DryRun)
		if n == 0 {
			return StepManual, "no feed switcher on the web layout · flip Home/Popular in the app"
		}
		return StepDone, fmt.Sprintf("switched the feed %d times", n)
	})

	step("Profile Perfectionist", "set a profile banner and description", func() (StepStatus, string) {
		if o.About == "" && o.Banner == "" {
			return StepManual, "pass --about and --banner, or set them at reddit.com/settings/profile"
		}
		return s.setProfile(who, o.About, o.Banner, cfg.DryRun)
	})

	step("Secured Account", "verify an email or phone number", func() (StepStatus, string) {
		return StepManual, "needs a code only you receive · reddit.com/settings/account"
	})

	return nil
}

type joinOutcome int

const (
	joinFailed joinOutcome = iota
	joinedNow
	joinedBefore
)

// findJoinJS digs out the join control **for a named subreddit**. A subreddit
// page is littered with join buttons for sidebar recommendations, so matching
// the first one joins something you never asked for · the element carries the
// community it belongs to, and that is what we match on.
const findJoinJS = deepJS + `(name) => {
  const want = String(name).toLowerCase().replace(/^r\//, '');
  const owns = (el) => {
    const n = (el.getAttribute('name') || '').toLowerCase();
    const p = (el.getAttribute('prefixed-name') || '').toLowerCase().replace(/^r\//, '');
    return n === want || p === want;
  };
  const hosts = deepAll((el) => el.tagName === 'SHREDDIT-JOIN-BUTTON').filter(owns);
  const host = hosts.filter(visible)[0] || hosts[0];
  if (!host) return null;
  // the real button lives in the element's shadow root, and so does its label
  return deepAll((c) => c.tagName === 'BUTTON' || c.getAttribute('role') === 'button', host)[0] || host;
}`

// joinSub opens a subreddit and joins it, unless you already are a member.
func (s *Session) joinSub(name string, dry bool) joinOutcome {
	url := "https://www.reddit.com/r/" + strings.TrimPrefix(strings.TrimSpace(name), "r/") + "/"

	if err := s.Goto(url); err != nil {
		return joinFailed
	}
	s.WaitForFeed(30*time.Second, nil)

	sub := strings.TrimPrefix(strings.TrimSpace(name), "r/")
	h, err := s.Page.EvaluateHandle(findJoinJS, sub)
	if err != nil {
		return joinFailed
	}
	btn := h.AsElement()
	if btn == nil {
		return joinFailed
	}
	// the label sits inside the shadow root · "Joined" means we are done here
	if l := strings.ToLower(s.label(h)); strings.Contains(l, "joined") || strings.Contains(l, "leave") {
		return joinedBefore
	}
	if dry {
		return joinedNow // a rehearsal counts the button it would have pressed
	}
	if err := btn.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(8000)}); err != nil {
		return joinFailed
	}
	s.Page.WaitForTimeout(1200)
	return joinedNow
}

// findResultJS picks the first real search result link on the results page,
// skipping the chrome reddit wraps around it.
const findResultJS = deepJS + `() => {
  const hits = deepAll((el) => {
    if (el.tagName !== 'A') return false;
    const href = el.getAttribute('href') || '';
    if (!/^\/r\/[^\/]+/.test(href)) return false;
    if (el.closest('nav, header, reddit-sidebar-nav, #left-sidebar')) return false;
    return true;
  }).filter(visible);
  return hits[0] || null;
}`

// clickSearchResult searches for a term and clicks one result · the badge
// counts result clicks, so each term contributes one.
func (s *Session) clickSearchResult(term string, dry bool) bool {
	url := "https://www.reddit.com/search/?q=" + strings.ReplaceAll(strings.TrimSpace(term), " ", "+")
	if _, err := s.Page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return false
	}
	s.Page.WaitForTimeout(2000)
	s.Dismiss()

	var link playwright.ElementHandle
	for i := 0; i < 10 && link == nil; i++ {
		if h, err := s.Page.EvaluateHandle(findResultJS); err == nil {
			link = h.AsElement()
		}
		if link == nil {
			s.Page.WaitForTimeout(400)
		}
	}
	if link == nil {
		return false
	}
	if dry {
		return true
	}
	if err := link.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(8000)}); err != nil {
		return false
	}
	s.Page.WaitForTimeout(1500)
	return true
}

// findFeedSwitchJS looks for the home feed's type control. The web layout
// spells it as a sort dropdown rather than the app's Home/Popular tabs, so
// this may find nothing · the caller reports that as a manual step.
const findFeedSwitchJS = deepJS + `(wanted) => {
  const rx = new RegExp(wanted, 'i');
  const hits = deepAll((el) => {
    const tag = el.tagName.toLowerCase();
    if (tag !== 'button' && tag !== 'a' && el.getAttribute('role') !== 'menuitem') return false;
    const t = text(el);
    if (!t || t.length > 20) return false;
    return rx.test(t);
  }).filter(visible);
  return hits[0] || null;
}`

// cycleFeedType flips the home feed between its types and back, and reports
// how many switches landed.
func (s *Session) cycleFeedType(dry bool) int {
	if err := s.Goto(RedditHome); err != nil {
		return 0
	}
	s.WaitForFeed(45*time.Second, nil)

	switched := 0
	for _, want := range []string{"popular", "latest", "best|home"} {
		h, err := s.Page.EvaluateHandle(findFeedSwitchJS, want)
		if err != nil {
			continue
		}
		el := h.AsElement()
		if el == nil {
			continue
		}
		if dry {
			switched++
			continue
		}
		if err := el.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(6000)}); err != nil {
			continue
		}
		switched++
		s.Page.WaitForTimeout(2000)
		s.Dismiss()
	}
	return switched
}

// setProfile fills in whatever you handed over · it never invents a bio or
// picks a picture for you.
func (s *Session) setProfile(who, about, banner string, dry bool) (StepStatus, string) {
	if _, err := s.Page.Goto("https://www.reddit.com/settings/profile", playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateDomcontentloaded,
	}); err != nil {
		return StepFailed, err.Error()
	}
	s.Page.WaitForTimeout(2500)
	s.Dismiss()

	var did []string
	if about != "" {
		if dry {
			did = append(did, "would set the description")
		} else if s.fillAbout(about) {
			did = append(did, "description set")
		}
	}
	if banner != "" {
		if dry {
			did = append(did, "would upload the banner")
		} else if s.uploadBanner(banner) {
			did = append(did, "banner uploaded")
		}
	}
	if len(did) == 0 {
		return StepManual, "could not find the profile fields · set them at reddit.com/settings/profile"
	}
	if about == "" || banner == "" {
		return StepDone, strings.Join(did, ", ") + " · the badge needs both"
	}
	return StepDone, strings.Join(did, ", ")
}

const findAboutJS = deepJS + `() => {
  const hits = deepAll((el) => {
    if (el.tagName !== 'TEXTAREA') return false;
    const hint = (el.getAttribute('name') || el.getAttribute('aria-label') || el.getAttribute('placeholder') || '').toLowerCase();
    return /about|description|bio/.test(hint) || true;
  }).filter(visible);
  return hits[0] || null;
}`

func (s *Session) fillAbout(about string) bool {
	h, err := s.Page.EvaluateHandle(findAboutJS)
	if err != nil {
		return false
	}
	el := h.AsElement()
	if el == nil {
		return false
	}
	if err := el.Fill(about); err != nil {
		return false
	}
	s.Page.WaitForTimeout(800)
	s.clickSave()
	return true
}

func (s *Session) uploadBanner(path string) bool {
	inputs, err := s.Page.QuerySelectorAll(`input[type="file"]`)
	if err != nil || len(inputs) == 0 {
		return false
	}
	// the banner input is the one that takes images; try each until one sticks
	for _, in := range inputs {
		if err := in.SetInputFiles([]string{path}); err == nil {
			s.Page.WaitForTimeout(2500)
			s.clickSave()
			return true
		}
	}
	return false
}

const findSaveJS = deepJS + `() => {
  const hits = deepAll((el) => {
    if (el.tagName !== 'BUTTON' && el.getAttribute('role') !== 'button') return false;
    const t = text(el);
    return t.length < 20 && /^save/i.test(t);
  }).filter(visible);
  return hits[0] || null;
}`

func (s *Session) clickSave() {
	if h, err := s.Page.EvaluateHandle(findSaveJS); err == nil {
		if el := h.AsElement(); el != nil {
			_ = el.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(6000)})
			s.Page.WaitForTimeout(1500)
		}
	}
}
