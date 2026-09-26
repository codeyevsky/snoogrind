package engine

import (
	"context"
	"fmt"
	"math/rand"
	"time"
)

// ScrollStats is how far a doomscroll session has travelled.
type ScrollStats struct {
	Pixels  int64 // cumulative, across reloads
	Flicks  int   // individual scroll gestures
	Posts   int   // distinct posts that went past
	Reloads int   // times the feed was reopened after it stalled
	Elapsed time.Duration
	Stopped string

	// BananaCM is the length this session counted a banana as.
	BananaCM float64
}

// Pixels are CSS pixels, and a CSS inch is 96 of them by definition.
const pxPerCM = 96 / 2.54

// Centimetres is the scrolled distance in the units the achievements think in.
func (s ScrollStats) Centimetres() float64 { return float64(s.Pixels) / pxPerCM }

func (s ScrollStats) bananaCM() float64 {
	if s.BananaCM > 0 {
		return s.BananaCM
	}
	return 18
}

// Bananas converts the distance into reddit's own unit of measurement. Reddit
// has never said how long its banana is · this is an estimate, and only our
// own bookkeeping: the badge counter lives on reddit's side.
func (s ScrollStats) Bananas() float64 { return s.Centimetres() / s.bananaCM() }

// Distance renders the travelled distance the way a human would say it.
func (s ScrollStats) Distance() string {
	cm := s.Centimetres()
	switch {
	case cm < 100:
		return fmt.Sprintf("%.0f cm", cm)
	case cm < 100000:
		return fmt.Sprintf("%.1f m", cm/100)
	default:
		return fmt.Sprintf("%.2f km", cm/100000)
	}
}

type ScrollOptions struct {
	Cfg Config
	For time.Duration // 0 runs until you stop it
	Ctl *Control
	On  func(ScrollStats, string) // stats plus a one line status
}

// Doomscroll walks the feed downward and does nothing else · reddit's distance
// achievements count how far the page has moved, so the whole job is to keep
// the feed travelling at a believable pace for as long as you asked.
func (s *Session) Doomscroll(ctx context.Context, o ScrollOptions) (*ScrollStats, error) {
	cfg := o.Cfg
	st := &ScrollStats{BananaCM: cfg.BananaCM}
	start := time.Now()

	emit := func(status string) {
		if o.On == nil {
			return
		}
		st.Elapsed = time.Since(start)
		o.On(*st, status)
	}

	url := cfg.URL()
	emit("opening " + url)
	if err := s.Goto(url); err != nil {
		return nil, err
	}
	if n := s.WaitForFeed(90*time.Second, func(m string) { emit(m) }); n == 0 {
		return nil, fmt.Errorf("no posts rendered · reddit is still gating this browser profile")
	}
	// Signed out the distance counts for nothing, so a real session insists on
	// an account · --dry-run stays open as a way to rehearse the pacing.
	if !cfg.DryRun && !s.LoggedIn() {
		return nil, fmt.Errorf("not signed in · run the login screen first")
	}

	s.CenterMouse()

	var deadline time.Time
	if o.For > 0 {
		deadline = start.Add(o.For)
	}
	seen := map[string]bool{}
	stalled := 0

	for {
		select {
		case <-ctx.Done():
			st.Stopped = "interrupted"
		default:
		}
		if st.Stopped == "" && o.Ctl.Stopped() {
			st.Stopped = "stopped by you"
		}
		if st.Stopped == "" && !deadline.IsZero() && time.Now().After(deadline) {
			st.Stopped = "reached the time you asked for"
		}
		if st.Stopped != "" {
			break
		}

		if o.Ctl.Paused() {
			emit("paused")
			time.Sleep(200 * time.Millisecond)
			continue
		}

		step := cfg.ScrollStepMin
		if cfg.ScrollStepMax > cfg.ScrollStepMin {
			step += rand.Intn(cfg.ScrollStepMax - cfg.ScrollStepMin)
		}
		moved, atBottom := s.ScrollBy(step)

		if moved > 0 {
			st.Pixels += int64(moved)
			st.Flicks++
			stalled = 0
		} else {
			stalled++
		}

		// Count what has gone past · cheap enough at this cadence, and it is
		// the honest signal that the feed is still feeding us something new.
		if st.Flicks%6 == 0 {
			if posts, err := s.Posts(); err == nil {
				for _, p := range posts {
					if !seen[p.ID] {
						seen[p.ID] = true
						st.Posts++
					}
				}
			}
		}

		switch {
		case stalled >= 25:
			// The feed gave up · reopen it and carry on from the top.
			emit("feed stalled · reopening it")
			if err := s.Goto(url); err != nil {
				return st, err
			}
			s.WaitForFeed(60*time.Second, nil)
			st.Reloads++
			stalled = 0
		case atBottom && moved == 0:
			emit("waiting for the next batch to load")
			s.Dismiss()
			time.Sleep(900 * time.Millisecond)
		default:
			emit(fmt.Sprintf("scrolling · %s travelled", st.Distance()))
		}

		if st.Flicks%40 == 0 {
			s.Dismiss()
		}
		time.Sleep(scrollPause(cfg))
	}

	st.Elapsed = time.Since(start)
	emit(st.Stopped)

	if cfg.DryRun {
		return st, nil // a rehearsal should not land in the distance totals
	}
	state := LoadState()
	state.Scrolls = append(state.Scrolls, ScrollRun{
		At:      time.Now().UTC().Format(time.RFC3339),
		Feed:    url,
		Pixels:  st.Pixels,
		Seconds: int(st.Elapsed.Seconds()),
		Stopped: st.Stopped,
	})
	if len(state.Scrolls) > 200 {
		state.Scrolls = state.Scrolls[len(state.Scrolls)-200:]
	}
	_ = state.Save()
	return st, nil
}

func scrollPause(c Config) time.Duration {
	lo := time.Duration(c.ScrollDelayMin) * time.Millisecond
	hi := time.Duration(c.ScrollDelayMax) * time.Millisecond
	return jitter(lo, hi)
}
