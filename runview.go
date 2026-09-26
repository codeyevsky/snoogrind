// The live run screen · the feed walk drawn as it happens, with pause and
// stop wired to the keyboard.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codeyevsky/snoogrind/internal/engine"
	"github.com/codeyevsky/snoogrind/internal/style"
)

// runRow is one finished post, as the feed list draws it.
type runRow struct {
	mark  string
	color string
	label string
	note  string
}

func rowFor(e engine.Event) runRow {
	switch e.Kind {
	case engine.EvShared:
		if e.Res.Verified {
			return runRow{"[✓]", style.Green, e.Post.Label(), "link copied"}
		}
		return runRow{"[+]", style.Green, e.Post.Label(), "shared, copy unconfirmed"}
	case engine.EvSkipped:
		return runRow{" · ", style.Gray, e.Post.Label(), "already shared"}
	case engine.EvFailed:
		return runRow{"[?]", style.Yellow, e.Post.Label(), e.Res.Err}
	case engine.EvDry:
		return runRow{"[~]", style.Cyan, e.Post.Label(), "dry run"}
	}
	return runRow{}
}

type runView struct {
	cfg      engine.Config
	user     string
	lifetime int // shares already on record before this run
	rows     []runRow
	status   string
	stats    engine.RunStats
	start    time.Time
	paused   bool
	done     bool
	err      error
	stopped  string
}

func (v *runView) draw() {
	f := newFrame()

	state := style.Tint(style.Green, "running")
	switch {
	case v.err != nil:
		state = style.Tint(style.Red, "failed")
	case v.done:
		state = style.Tint(style.Orange, "done")
	case v.paused:
		state = style.Tint(style.Yellow, "paused")
	}
	header(f, state, time.Since(v.start).Truncate(time.Second).String())

	// counters
	who := v.user
	if who == "" {
		who = "—"
	}
	f.addf("  %s  %s", style.Tint(style.Dim, "u/"+who), style.Tint(style.Dim, v.cfg.URL()))
	f.addf("  %s  %s  %s  %s  %s",
		count("shared", v.stats.Shared, style.Green),
		count("verified", v.stats.Verified, style.Green),
		count("seen", v.stats.Seen, style.Gray),
		count("skipped", v.stats.Skipped, style.Gray),
		count("failed", v.stats.Failed, style.Yellow))

	width := f.w - 22
	if width < 10 {
		width = 10
	}
	f.addf("  %s %s", bar(v.stats.Shared, v.cfg.MaxShares, width),
		style.Tint(style.Dim, fmt.Sprintf("%d/%d · %d scrolls", v.stats.Shared, v.cfg.MaxShares, v.stats.Scrolls)))
	f.add("  " + style.Tint(style.Dim, engine.Progress(engine.ShareTiers, int64(v.lifetime+v.stats.Shared))))
	f.rule("─", style.Rust)

	// the feed list fills whatever is left between header and footer
	body := f.h - len(f.lines) - 4
	if body < 1 {
		body = 1
	}
	rows := v.rows
	if len(rows) > body {
		rows = rows[len(rows)-body:]
	}
	for _, r := range rows {
		note := ""
		if r.note != "" {
			note = "  " + style.Tint(style.Dim, r.note)
		}
		f.addf("  %s %s%s", style.Tint(r.color, r.mark), r.label, note)
	}

	f.pad(f.h - 3)
	f.rule("─", style.Rust)
	switch {
	case v.err != nil:
		f.add("  " + style.Tint(style.Red, "x "+v.err.Error()))
	case v.done:
		f.add("  " + style.Tint(style.Orange, "stopped: "+v.stopped))
	default:
		f.add("  " + style.Tint(style.Dim, v.status))
	}
	if v.done || v.err != nil {
		f.add(style.Tint(style.Dim, "  any key back to the menu"))
	} else {
		f.add(style.Tint(style.Dim, "  p pause · s stop after this post · q quit"))
	}
	f.paint()
}

// apply folds one engine event into the view.
func (v *runView) apply(e engine.Event) {
	v.stats = e.Stats
	switch e.Kind {
	case engine.EvStatus:
		v.status = e.Text
		if who, ok := strings.CutPrefix(e.Text, "signed in as u/"); ok {
			v.user = who
		}
	case engine.EvStopped:
		v.stopped = e.Text
	case engine.EvScroll:
		// the counters already moved; there is nothing to list
	default:
		if r := rowFor(e); r.mark != "" {
			v.rows = append(v.rows, r)
			if len(v.rows) > 400 {
				v.rows = v.rows[len(v.rows)-400:]
			}
		}
	}
}

func count(label string, n int, color string) string {
	return style.Tint(color, fmt.Sprintf("%d", n)) + style.Tint(style.Dim, " "+label)
}

// screenRun drives a whole walk from the alternate screen: the engine runs in
// the background and reports events, the keyboard steers it, and the view
// repaints on a ticker so the elapsed clock keeps moving even when the browser
// is between posts.
func screenRun(ctx context.Context, cfg engine.Config) error {
	if keys == nil {
		return cmdRun(ctx, cfg) // no raw terminal · fall back to plain output
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctl := &engine.Control{}
	events := make(chan engine.Event, 256)
	type result struct {
		stats *engine.RunStats
		err   error
	}
	done := make(chan result, 1)

	v := &runView{cfg: cfg, start: time.Now(), status: "launching " + cfg.Browser + "…"}
	st0 := engine.LoadState()
	v.user, v.lifetime = st0.User, len(st0.Shared)
	v.draw()

	go func() {
		defer close(events)
		s, err := engine.Open(engine.Opts{Browser: cfg.Browser, Headless: cfg.Headless, ProfileDir: cfg.ProfileDir})
		if err != nil {
			done <- result{nil, err}
			return
		}
		defer s.Close()
		if who := s.Who(); who != "" {
			events <- engine.Event{Kind: engine.EvStatus, Text: "signed in as u/" + who}
		}
		stats, err := s.Walk(ctx, engine.RunOptions{
			Cfg: cfg,
			Ctl: ctl,
			On:  func(e engine.Event) { events <- e },
		})
		done <- result{stats, err}
	}()

	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			v.apply(e)

		case r := <-done:
			// The walk finishes while its last events are still queued, and
			// select picks a ready case at random · drain them or the list
			// loses whatever landed in the final moments.
			for len(events) > 0 {
				v.apply(<-events)
			}
			v.done, v.err = true, r.err
			if r.stats != nil {
				v.stats = *r.stats
				if v.stopped == "" {
					v.stopped = r.stats.Stopped
				}
			}
			if v.stopped == "" {
				v.stopped = "finished"
			}
			v.draw()
			key() // any key goes back to the menu
			return nil

		case b, ok := <-keys.ch:
			if !ok {
				return nil
			}
			switch b {
			case 'p':
				ctl.TogglePause()
				v.paused = ctl.Paused()
			case 's':
				ctl.Stop()
				v.status = "stopping after this post…"
			case 'q', 3:
				ctl.Stop()
				cancel()
				v.status = "quitting…"
			}

		case <-tick.C:
		}
		v.draw()
	}
}
