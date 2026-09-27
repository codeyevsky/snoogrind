// The doomscroll screen · distance travelled, drawn live, with nothing else
// happening on the page.

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/codeyevsky/snoogrind/internal/engine"
	"github.com/codeyevsky/snoogrind/internal/style"
)

type scrollView struct {
	cfg    engine.Config
	user   string
	want   time.Duration
	st     engine.ScrollStats
	status string
	start  time.Time
	paused bool
	done   bool
	err    error
}

func (v *scrollView) draw() {
	f := newFrame()

	state := style.Tint(style.Green, "scrolling")
	switch {
	case v.err != nil:
		state = style.Tint(style.Red, "failed")
	case v.done:
		state = style.Tint(style.Orange, "done")
	case v.paused:
		state = style.Tint(style.Yellow, "paused")
	}
	elapsed := time.Since(v.start).Truncate(time.Second)
	right := elapsed.String()
	if v.want > 0 && !v.done {
		right = fmt.Sprintf("%s / %s", elapsed, v.want)
	}
	header(f, state, right)

	who := v.user
	if who == "" {
		who = ","
	}
	f.addf("  %s  %s", style.Tint(style.Dim, "u/"+who), style.Tint(style.Dim, v.cfg.URL()))
	f.blank()

	// the distance is the headline, so give it room
	f.addf("   %s  %s",
		style.Tint(style.Orange+";1", v.st.Distance()),
		style.Tint(style.Dim, "travelled"))
	f.blank()
	f.addf("  %s  %s  %s  %s",
		count("flicks", v.st.Flicks, style.Gray),
		count("posts seen", v.st.Posts, style.Gray),
		count("reloads", v.st.Reloads, style.Gray),
		style.Tint(style.Dim, fmt.Sprintf("%d px", v.st.Pixels)))

	if v.want > 0 {
		secs := int(elapsed.Seconds())
		total := int(v.want.Seconds())
		f.blank()
		f.addf("  %s %s", bar(secs, total, f.w-34),
			style.Tint(style.Dim, fmt.Sprintf("%d%% of %s", min(secs*100/maxLen(total, 1), 100), v.want)))
	}

	f.pad(f.h - 3)
	f.rule("─", style.Rust)
	switch {
	case v.err != nil:
		f.add("  " + style.Tint(style.Red, "x "+v.err.Error()))
	case v.done:
		f.add("  " + style.Tint(style.Orange, "stopped: "+v.st.Stopped))
	default:
		f.add("  " + style.Tint(style.Dim, v.status))
	}
	if v.done || v.err != nil {
		f.add(style.Tint(style.Dim, "  any key back to the menu"))
	} else {
		f.add(style.Tint(style.Dim, "  p pause · s stop · q quit"))
	}
	f.paint()
}

// screenScroll runs a doomscroll session behind the live distance view.
func screenScroll(ctx context.Context, cfg engine.Config) error {
	if keys == nil {
		return cmdScroll(ctx, cfg)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctl := &engine.Control{}
	type update struct {
		st     engine.ScrollStats
		status string
	}
	updates := make(chan update, 64)
	type result struct {
		st  *engine.ScrollStats
		err error
	}
	done := make(chan result, 1)

	v := &scrollView{
		cfg:    cfg,
		want:   cfg.ScrollDuration(),
		start:  time.Now(),
		status: "launching " + cfg.Browser + "…",
		user:   engine.LoadState().User,
	}
	v.draw()

	go func() {
		defer close(updates)
		s, err := openSession(cfg)
		if err != nil {
			done <- result{nil, err}
			return
		}
		defer s.Close()
		st, err := s.Doomscroll(ctx, engine.ScrollOptions{
			Cfg: cfg,
			For: v.want,
			Ctl: ctl,
			On: func(st engine.ScrollStats, status string) {
				// dropping a status line is fine · the next one is 300ms away
				select {
				case updates <- update{st, status}:
				default:
				}
			},
		})
		done <- result{st, err}
	}()

	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case u, ok := <-updates:
			if !ok {
				updates = nil
				continue
			}
			v.st, v.status = u.st, u.status

		case r := <-done:
			for len(updates) > 0 {
				u := <-updates
				v.st, v.status = u.st, u.status
			}
			v.done, v.err = true, r.err
			if r.st != nil {
				v.st = *r.st
			}
			if v.st.Stopped == "" {
				v.st.Stopped = "finished"
			}
			v.draw()
			key()
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
				v.status = "stopping…"
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
