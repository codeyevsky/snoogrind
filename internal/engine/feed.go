package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// deepJS is prepended to every page script · reddit's feed is built from custom
// elements, so anything that only walks the light DOM misses half the buttons.
const deepJS = `
const deepAll = (pred, root) => {
  const out = [];
  const stack = [root || document];
  while (stack.length) {
    const node = stack.pop();
    const kids = node.children ? Array.from(node.children) : [];
    for (const k of kids) {
      if (pred(k)) out.push(k);
      if (k.shadowRoot) stack.push(k.shadowRoot);
      stack.push(k);
    }
  }
  return out;
};
const visible = (el) => {
  const r = el.getBoundingClientRect();
  if (r.width < 1 || r.height < 1) return false;
  const st = getComputedStyle(el);
  return st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
};
const text = (el) => (el.textContent || '').replace(/\s+/g, ' ').trim();
`

// Post is one feed entry, identified by reddit's own t3_ fullname.
type Post struct {
	ID        string `json:"id"`
	Permalink string `json:"permalink"`
	Title     string `json:"title"`
	Sub       string `json:"sub"`
}

// Label is what a run prints for this post.
func (p Post) Label() string {
	t := strings.TrimSpace(p.Title)
	if t == "" {
		t = p.Permalink
	}
	if len([]rune(t)) > 52 {
		t = string([]rune(t)[:51]) + "…"
	}
	sub := strings.TrimSpace(p.Sub)
	if sub == "" {
		return t
	}
	return sub + " · " + t
}

const collectJS = deepJS + `() => {
  const out = [];
  const push = (id, permalink, title, sub) => {
    if (!id || !id.startsWith('t3_')) return;
    out.push({ id, permalink: permalink || '', title: title || '', sub: sub || '' });
  };
  for (const p of document.querySelectorAll('shreddit-post')) {
    push(p.getAttribute('id'),
         p.getAttribute('permalink'),
         p.getAttribute('post-title') || p.getAttribute('aria-label'),
         p.getAttribute('subreddit-prefixed-name'));
  }
  if (out.length) return out;
  // older / logged out feed markup
  for (const el of document.querySelectorAll('[id^="t3_"]')) {
    const a = el.querySelector('a[href*="/comments/"]');
    const h = el.querySelector('h1, h2, h3, [slot="title"]');
    const s = el.querySelector('a[href^="/r/"]');
    push(el.getAttribute('id'), a ? a.getAttribute('href') : '', h ? text(h) : '', s ? text(s) : '');
  }
  return out;
}`

// Posts reads every post currently rendered in the feed, in feed order.
func (s *Session) Posts() ([]Post, error) {
	raw, err := s.Page.Evaluate(collectJS)
	if err != nil {
		return nil, err
	}
	list, _ := raw.([]any)
	out := make([]Post, 0, len(list))
	seen := map[string]bool{}
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var p Post
		p.ID, _ = m["id"].(string)
		p.Permalink, _ = m["permalink"].(string)
		p.Title, _ = m["title"].(string)
		p.Sub, _ = m["sub"].(string)
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out, nil
}

// findShareJS locates one post's share control. The action bar is not a child
// of <shreddit-post>, reddit renders it as a sibling, so this matches on the
// post's own fullname, which <shreddit-post-share-button> carries as source-id,
// and then digs the real <button> out of the element's shadow root.
const findShareJS = deepJS + `({ id, permalink }) => {
  const rx = /\bshare\b|payla/i;
  const owns = (el) => {
    if (id && el.getAttribute('source-id') === id) return true;
    if (id && el.getAttribute('post-id') === id) return true;
    return !!permalink && el.getAttribute('permalink') === permalink;
  };
  let hosts = deepAll((el) => el.tagName === 'SHREDDIT-POST-SHARE-BUTTON').filter(owns);
  if (!hosts.length) {
    // whatever reddit renames the element to, the action still tags itself
    hosts = deepAll((el) =>
      rx.test(el.getAttribute('data-action-bar-action') || el.getAttribute('data-post-click-location') || el.getAttribute('noun') || '')
    ).filter(owns);
  }
  if (!hosts.length) return null;
  const host = hosts.filter(visible)[0] || hosts[0];
  const inner = deepAll((c) => c.tagName === 'BUTTON' || c.getAttribute('role') === 'button', host);
  return inner.filter(visible)[0] || inner[0] || host;
}`

// findCopyJS locates the "Copy link" entry of the open share menu. The menu is
// portalled out of the post, so this searches the whole document and keeps the
// innermost match (the <button>, not the <li> wrapping it).
const findCopyJS = deepJS + `() => {
  const exact = /^(copy link|copy post link|link kopyala|bağlantıyı kopyala)$/i;
  const loose = /copy link|link kopyala|bağlantıyı kopyala/i;
  const isItem = (el) => {
    const tag = el.tagName.toLowerCase();
    if (tag !== 'button' && tag !== 'a' && tag !== 'li' && el.getAttribute('role') !== 'menuitem') return false;
    const t = text(el);
    const label = el.getAttribute('aria-label') || '';
    if (t.length > 40 && !loose.test(label)) return false;
    return exact.test(t) || loose.test(label) || (loose.test(t) && t.length <= 40);
  };
  const hits = deepAll(isItem).filter(visible);
  const inner = hits.filter((el) => !hits.some((o) => o !== el && el.contains(o)));
  return inner[0] || hits[0] || null;
}`

const clipboardJS = `async () => {
  try { return await navigator.clipboard.readText(); } catch (e) { return ""; }
}`

// toastJS looks for the "Copied" confirmation reddit flashes after a copy · the
// fallback signal on firefox, where the clipboard stays out of reach.
const toastJS = deepJS + `() => {
  const rx = /copied|kopyalandı/i;
  return deepAll((el) => el.children.length === 0 && rx.test(text(el)) && text(el).length < 40).some(visible);
}`

// scrollProbeJS reports where the page sits · the window is shreddit's
// scroller, and scrollY is what every distance measurement reads.
const scrollProbeJS = `() => ({
  y: window.scrollY,
  bottom: window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2,
})`

const scrollByJS = `(d) => { window.scrollBy(0, d); return window.scrollY; }`

// ShareResult is the outcome of one share → copy link pass over a post.
type ShareResult struct {
	OK       bool
	Verified bool // the clipboard (or the toast) confirmed the copy
	Link     string
	Err      string
}

func (s *Session) clipboard() string {
	v, err := s.Page.Evaluate(clipboardJS)
	if err != nil {
		return ""
	}
	t, _ := v.(string)
	return t
}

// findShare polls for the post's share control, returning as soon as the feed
// has hydrated one · the action bar arrives a beat after the card itself.
func (s *Session) findShare(p Post, tries int) playwright.ElementHandle {
	arg := map[string]any{"id": p.ID, "permalink": p.Permalink}
	for i := 0; i < tries; i++ {
		if h, err := s.Page.EvaluateHandle(findShareJS, arg); err == nil {
			if el := h.AsElement(); el != nil {
				return el
			}
		}
		s.Page.WaitForTimeout(200)
	}
	return nil
}

// handleFor resolves a post back to a live element handle · the feed redraws
// as it loads, so we look it up again right before acting on it.
func (s *Session) handleFor(p Post) playwright.ElementHandle {
	for _, sel := range []string{
		fmt.Sprintf("shreddit-post[id=%q]", p.ID),
		fmt.Sprintf("[id=%q]", p.ID),
	} {
		if h, err := s.Page.QuerySelector(sel); err == nil && h != nil {
			return h
		}
	}
	return nil
}

// Share performs the full share → copy link gesture on one post with real
// mouse clicks, then confirms the link actually landed on the clipboard.
func (s *Session) Share(p Post) ShareResult {
	post := s.handleFor(p)
	if post == nil {
		return ShareResult{Err: "post left the page before we reached it"}
	}
	if err := post.ScrollIntoViewIfNeeded(playwright.ElementHandleScrollIntoViewIfNeededOptions{
		Timeout: playwright.Float(8000),
	}); err != nil {
		return ShareResult{Err: "could not scroll to post: " + short(err.Error())}
	}
	s.Page.WaitForTimeout(250)

	// The action bar hydrates lazily once the post is on screen, so give the
	// share button a couple of seconds to appear before calling it missing.
	share := s.findShare(p, 15)
	if share == nil {
		return ShareResult{Err: "no share button on this post"}
	}
	_ = share.ScrollIntoViewIfNeeded(playwright.ElementHandleScrollIntoViewIfNeededOptions{
		Timeout: playwright.Float(6000),
	})
	before := s.clipboard()
	if err := share.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(8000)}); err != nil {
		return ShareResult{Err: "share click failed: " + short(err.Error())}
	}

	// The menu is lazy loaded, so poll for the copy entry instead of guessing.
	var item playwright.ElementHandle
	for i := 0; i < 20 && item == nil; i++ {
		s.Page.WaitForTimeout(200)
		h, err := s.Page.EvaluateHandle(findCopyJS)
		if err != nil {
			continue
		}
		item = h.AsElement()
	}
	if item == nil {
		s.closeMenu()
		return ShareResult{Err: "share menu opened but had no copy link entry"}
	}
	if err := item.Click(playwright.ElementHandleClickOptions{Timeout: playwright.Float(8000)}); err != nil {
		s.closeMenu()
		return ShareResult{Err: "copy link click failed: " + short(err.Error())}
	}
	s.Page.WaitForTimeout(500)

	res := ShareResult{OK: true}
	after := s.clipboard()
	if after != "" && after != before && strings.Contains(after, "reddit.com") {
		res.Verified, res.Link = true, strings.TrimSpace(after)
	} else if v, err := s.Page.Evaluate(toastJS); err == nil {
		if ok, _ := v.(bool); ok {
			res.Verified = true
		}
	}
	s.closeMenu()
	return res
}

// closeMenu dismisses whatever the share click opened so the next post's button
// is clickable · Escape first, then a click on empty space as a backstop.
func (s *Session) closeMenu() {
	_ = s.Page.Keyboard().Press("Escape")
	s.Page.WaitForTimeout(150)
	if h, err := s.Page.EvaluateHandle(findCopyJS); err == nil && h.AsElement() != nil {
		_ = s.Page.Mouse().Click(6, 6)
		s.Page.WaitForTimeout(150)
	}
}

// WaitForFeed blocks until posts render. A blank browser profile gets reddit's
// "prove your humanity" interstitial first, which clears itself after roughly
// half a minute · this is what keeps a run from giving up during it.
func (s *Session) WaitForFeed(timeout time.Duration, say func(string)) int {
	deadline := time.Now().Add(timeout)
	warned := false
	for {
		if posts, err := s.Posts(); err == nil && len(posts) > 0 {
			return len(posts)
		}
		if time.Now().After(deadline) {
			return 0
		}
		if !warned && say != nil && s.challenged() {
			say("reddit is showing its human check · waiting for it to clear")
			warned = true
		}
		s.Page.WaitForTimeout(1500)
		s.Dismiss()
	}
}

const challengeJS = `() => /prove your humanity|blocked by network security|complete the challenge/i.test(document.body ? document.body.innerText.slice(0, 2000) : '')`

func (s *Session) challenged() bool {
	v, err := s.Page.Evaluate(challengeJS)
	if err != nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

// ScrollPos is the page's vertical offset, and whether it has hit the bottom.
func (s *Session) ScrollPos() (float64, bool) {
	v, err := s.Page.Evaluate(scrollProbeJS)
	if err != nil {
		return 0, false
	}
	m, _ := v.(map[string]any)
	bottom, _ := m["bottom"].(bool)
	return num(m["y"]), bottom
}

// ScrollBy moves the feed down by px with a real wheel event, the gesture a
// person makes, and reports how far the page actually travelled and whether
// it has reached the bottom. When the wheel lands somewhere that does not
// scroll, it falls back to a scripted scroll so a run never silently stalls.
func (s *Session) ScrollBy(px int) (moved float64, atBottom bool) {
	before, _ := s.ScrollPos()
	if err := s.Page.Mouse().Wheel(0, float64(px)); err == nil {
		s.Page.WaitForTimeout(80)
		after, bottom := s.ScrollPos()
		if after > before {
			return after - before, bottom
		}
	}
	if v, err := s.Page.Evaluate(scrollByJS, px); err == nil {
		after := num(v)
		_, bottom := s.ScrollPos()
		return after - before, bottom
	}
	return 0, false
}

// CenterMouse parks the pointer over the middle of the feed, so wheel events
// go to the page and not to a sidebar that happens to sit under (0, 0).
func (s *Session) CenterMouse() {
	v, err := s.Page.Evaluate(`() => [window.innerWidth / 2, window.innerHeight / 2]`)
	if err != nil {
		return
	}
	xy, _ := v.([]any)
	if len(xy) == 2 {
		_ = s.Page.Mouse().Move(num(xy[0]), num(xy[1]))
	}
}

// Scroll advances the feed one screen and reports whether the page moved.
func (s *Session) Scroll() bool {
	v, err := s.Page.Evaluate(`() => Math.round(window.innerHeight * 0.9)`)
	if err != nil {
		return false
	}
	moved, _ := s.ScrollBy(int(num(v)))
	s.Page.WaitForTimeout(900)
	return moved > 0
}

// num reads a JavaScript number out of an Evaluate result. Playwright hands
// whole numbers back as int and fractions as float64, so asserting one of them
// silently loses the other · every numeric read goes through here.
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case int32:
		return float64(n)
	}
	return 0
}

func short(s string) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len([]rune(s)) > 90 {
		s = string([]rune(s)[:89]) + "…"
	}
	return s
}

// labelJS reads whatever a control calls itself · its aria-label, its text, or
// failing both its tag name.
const labelJS = deepJS + `(el) => el ? ((el.getAttribute('aria-label') || text(el) || el.tagName).slice(0, 60)) : ""`

// label is how a run names the control it is about to press.
func (s *Session) label(h playwright.JSHandle) string {
	v, err := s.Page.Evaluate(labelJS, h)
	if err != nil {
		return ""
	}
	t, _ := v.(string)
	return strings.TrimSpace(t)
}
