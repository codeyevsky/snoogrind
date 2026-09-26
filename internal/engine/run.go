package engine

import (
	"context"
	"fmt"
	"math/rand"
	"sync/atomic"
	"time"
)

// EventKind tags what a walk just did · the UI decides how to draw it, so the
// engine stays free of any terminal concerns.
type EventKind int

const (
	EvStatus  EventKind = iota // Text: what the run is busy with
	EvShared                   // Post + Res: share -> copy link went through
	EvSkipped                  // Post: already on record from an earlier run
	EvFailed                   // Post + Res: the gesture did not complete
	EvDry                      // Post: dry run, nothing was touched
	EvScroll                   // Stats: the feed advanced one screen
	EvStopped                  // Text: why the walk ended
)

type Event struct {
	Kind  EventKind
	Text  string
	Post  Post
	Res   ShareResult
	Stats RunStats
}

// Control lets a live UI steer a walk that is already in flight.
type Control struct {
	paused atomic.Bool
	stop   atomic.Bool
}

func (c *Control) TogglePause() {
	if c != nil {
		c.paused.Store(!c.paused.Load())
	}
}

func (c *Control) Paused() bool { return c != nil && c.paused.Load() }

func (c *Control) Stop() {
	if c != nil {
		c.stop.Store(true)
	}
}

func (c *Control) Stopped() bool { return c != nil && c.stop.Load() }

type RunOptions struct {
	Cfg Config
	On  func(Event) // every step reports here; nil is allowed
	Ctl *Control    // optional pause / stop handle
}

type RunStats struct {
	Shared   int
	Verified int
	Skipped  int
	Failed   int
	Scrolls  int
	Seen     int // posts the walk has looked at
	Stopped  string
}

func jitter(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// tooMuchJS catches reddit's own throttle screen · when it shows up there is no
// point clicking further, the shares stop counting.
const tooMuchJS = `() => /you(')?re doing that too much|too many requests|rate ?limit/i.test(document.body ? document.body.innerText.slice(0, 4000) : '')`

func (s *Session) throttled() bool {
	v, err := s.Page.Evaluate(tooMuchJS)
	if err != nil {
		return false
	}
	b, _ := v.(bool)
	return b
}

// Walk opens the configured feed and runs share → copy link over every post it
// meets, scrolling for more until a limit, the end of the feed, or the UI says
// stop. It reports each step through o.On instead of printing anything itself.
func (s *Session) Walk(ctx context.Context, o RunOptions) (*RunStats, error) {
	cfg := o.Cfg
	state := LoadState()
	stats := &RunStats{}

	emit := func(kind EventKind, text string, p Post, res ShareResult) {
		if o.On == nil {
			return
		}
		o.On(Event{Kind: kind, Text: text, Post: p, Res: res, Stats: *stats})
	}
	say := func(text string) { emit(EvStatus, text, Post{}, ShareResult{}) }

	url := cfg.URL()
	say("opening " + url)
	if err := s.Goto(url); err != nil {
		return nil, err
	}
	if n := s.WaitForFeed(90*time.Second, say); n == 0 {
		return nil, fmt.Errorf("no posts rendered · reddit is still gating this browser profile")
	}
	// A dry run only reads the feed, so it works signed out · a real one does
	// not, because reddit only counts a share against an account.
	if !cfg.DryRun && !s.LoggedIn() {
		return nil, fmt.Errorf("not signed in · run the login screen first")
	}

	seen := map[string]bool{} // posts this run has already decided on
	emptyRounds := 0
	failStreak := 0

	// halted reports the reasons to leave the loop that can arrive at any
	// moment: the context, or the UI's stop key.
	halted := func() bool {
		select {
		case <-ctx.Done():
			stats.Stopped = "interrupted"
			return true
		default:
		}
		if o.Ctl.Stopped() {
			stats.Stopped = "stopped by you"
			return true
		}
		return false
	}

	// hold blocks while the UI has the run paused.
	hold := func() {
		for o.Ctl.Paused() && !halted() {
			time.Sleep(120 * time.Millisecond)
		}
	}

	for !halted() {
		posts, err := s.Posts()
		if err != nil {
			return stats, err
		}

		fresh := 0
		for _, p := range posts {
			hold()
			if halted() {
				break
			}
			if seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			stats.Seen++

			if _, done := state.Shared[p.ID]; done {
				stats.Skipped++
				emit(EvSkipped, "", p, ShareResult{})
				continue
			}
			fresh++

			if cfg.DryRun {
				stats.Shared++
				emit(EvDry, "", p, ShareResult{})
				if stats.Shared >= cfg.MaxShares {
					stats.Stopped = "reached the share limit"
					break
				}
				continue
			}

			say("sharing " + p.Label())
			res := s.Share(p)
			now := time.Now().UTC().Format(time.RFC3339)
			if res.OK {
				failStreak = 0
				stats.Shared++
				if res.Verified {
					stats.Verified++
				}
				state.Shared[p.ID] = Entry{At: now, Permalink: p.Permalink, Sub: p.Sub, Title: p.Title}
				delete(state.Failed, p.ID)
				_ = state.Save()
				emit(EvShared, "", p, res)
			} else {
				failStreak++
				stats.Failed++
				state.Failed[p.ID] = Entry{At: now, Permalink: p.Permalink, Sub: p.Sub, Title: p.Title, Reason: res.Err}
				_ = state.Save()
				emit(EvFailed, "", p, res)
			}

			if s.throttled() {
				stats.Stopped = "reddit is throttling · stopping"
				break
			}
			if failStreak >= 5 {
				stats.Stopped = fmt.Sprintf("%d posts in a row failed (layout changed, or a limit)", failStreak)
				break
			}
			if stats.Shared >= cfg.MaxShares {
				stats.Stopped = "reached the share limit"
				break
			}
			time.Sleep(jitter(cfg.Min(), cfg.Max()))
		}

		if stats.Stopped != "" {
			break
		}

		if fresh == 0 {
			emptyRounds++
			if emptyRounds >= 4 {
				stats.Stopped = "no new posts left in the feed"
				break
			}
		} else {
			emptyRounds = 0
		}

		// A zero scroll budget means "keep going" · the feed running dry is
		// then the only thing that ends a run short of the share limit.
		if cfg.MaxScrolls > 0 && stats.Scrolls >= cfg.MaxScrolls {
			stats.Stopped = "reached the scroll limit"
			break
		}
		stats.Scrolls++
		say("scrolling for more posts")
		if !s.Scroll() {
			emptyRounds++
		}
		s.Dismiss()
		emit(EvScroll, "", Post{}, ShareResult{})
	}

	if stats.Stopped == "" {
		stats.Stopped = "finished"
	}
	emit(EvStopped, stats.Stopped, Post{}, ShareResult{})

	state.Runs = append(state.Runs, Run{
		At:      time.Now().UTC().Format(time.RFC3339),
		Feed:    url,
		Shared:  stats.Shared,
		Skipped: stats.Skipped,
		Scrolls: stats.Scrolls,
		Stopped: stats.Stopped,
		DryRun:  cfg.DryRun,
	})
	if len(state.Runs) > 200 {
		state.Runs = state.Runs[len(state.Runs)-200:]
	}
	_ = state.Save()
	return stats, nil
}
