// The Getting Started screen · the seven newcomer badges as a live checklist.

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/codeyevsky/snoogrind/internal/engine"
	"github.com/codeyevsky/snoogrind/internal/style"
)

// mark renders a step's status as its symbol and colour.
func mark(st engine.StepStatus) (string, string) {
	switch st {
	case engine.StepRunning:
		return "[·]", style.Orange
	case engine.StepDone:
		return "[✓]", style.Green
	case engine.StepAlready:
		return "[✓]", style.Gray
	case engine.StepManual:
		return "[!]", style.Yellow
	case engine.StepFailed:
		return "[x]", style.Red
	}
	return "[ ]", style.Gray
}

type startRow struct {
	badge  string
	need   string
	status engine.StepStatus
	note   string
}

type startView struct {
	rows   []startRow
	start  time.Time
	done   bool
	err    error
	dryRun bool
}

// apply folds one step event in, replacing the row it belongs to so a running
// step turns into its result in place rather than stacking up.
func (v *startView) apply(e engine.StepEvent) {
	for i := range v.rows {
		if v.rows[i].badge == e.Badge {
			v.rows[i].status, v.rows[i].note = e.Status, e.Note
			return
		}
	}
	v.rows = append(v.rows, startRow{e.Badge, e.Need, e.Status, e.Note})
}

func (v *startView) draw() {
	f := newFrame()

	state := style.Tint(style.Green, "working through the list")
	switch {
	case v.err != nil:
		state = style.Tint(style.Red, "failed")
	case v.done:
		state = style.Tint(style.Orange, "done")
	}
	if v.dryRun {
		state += style.Tint(style.Cyan, " · dry run")
	}
	header(f, state, time.Since(v.start).Truncate(time.Second).String())
	f.blank()

	for _, r := range v.rows {
		m, c := mark(r.status)
		f.addf("  %s %s", style.Tint(c, m), r.badge)
		detail := r.need
		if r.note != "" {
			detail = r.note
		}
		f.add("      " + style.Tint(style.Dim, detail))
	}

	f.pad(f.h - 3)
	f.rule("─", style.Rust)
	switch {
	case v.err != nil:
		f.add("  " + style.Tint(style.Red, "x "+v.err.Error()))
	case v.done:
		f.add("  " + style.Tint(style.Dim, "[!] rows are yours to finish · everything else is earned"))
	default:
		f.add("  " + style.Tint(style.Dim, "reddit counts these from the app, so give them a minute to appear"))
	}
	if v.done || v.err != nil {
		f.add(style.Tint(style.Dim, "  any key back to the menu"))
	} else {
		f.add(style.Tint(style.Dim, "  s stop · q quit"))
	}
	f.paint()
}

// startSetup asks for the three things only a Getting Started run needs, as
// an editable list rather than a pile of command line flags.
func startSetup(cfg engine.Config) (engine.Config, engine.StartOptions, bool) {
	o := engine.StartOptions{Subs: append([]string(nil), engine.DefaultSubs...)}

	rows := []struct {
		name string
		show func() string
		edit func()
	}{
		{"subreddits", func() string { return strings.Join(o.Subs, ", ") }, func() {
			if v, ok := input("start", "subreddits to join, comma separated", strings.Join(o.Subs, ", ")); ok {
				o.Subs = nil
				for _, p := range strings.Split(v, ",") {
					if p = strings.TrimSpace(p); p != "" {
						o.Subs = append(o.Subs, p)
					}
				}
			}
		}},
		{"profile text", func() string { return orNotSet(o.About) }, func() {
			if v, ok := input("start", "your profile description (blank = skip the badge)", o.About); ok {
				o.About = strings.TrimSpace(v)
			}
		}},
		{"profile banner", func() string { return orNotSet(o.Banner) }, func() {
			if v, ok := input("start", "path to a banner image (blank = skip the badge)", o.Banner); ok {
				o.Banner = strings.TrimSpace(v)
			}
		}},
	}

	sel := 0
	for {
		f := newFrame()
		mode := style.Tint(style.Green, "for real")
		if cfg.DryRun {
			mode = style.Tint(style.Cyan, "rehearsal · nothing gets joined")
		}
		header(f, "start", "")
		f.blank()
		f.add("  " + style.Tint(style.Dim, "seven newcomer badges · this run is a ") + mode)
		f.blank()
		for i, r := range rows {
			line := fmt.Sprintf("%-15s %s", r.name, r.show())
			if i == sel {
				f.add("  " + style.Tint(style.Orange, "❯") + " " + style.Hl(line))
			} else {
				f.add("    " + style.Tint(style.Gray, line))
			}
		}
		f.blank()
		if sel == len(rows) {
			f.add("  " + style.Tint(style.Orange, "❯") + " " + style.Hl("▶ start"))
		} else {
			f.add("    " + style.Tint(style.Gray, "▶ start"))
		}
		footer(f, "  arrows move · Enter edit or start · d rehearse on/off · q cancel")
		f.paint()

		b, ok := key()
		if !ok {
			return cfg, o, false
		}
		switch b {
		case 'q', 3, 0x1b:
			return cfg, o, false
		case 'd':
			cfg.DryRun = !cfg.DryRun
		case '\r', '\n':
			if sel == len(rows) {
				return cfg, o, true
			}
			rows[sel].edit()
		case keyUp:
			if sel > 0 {
				sel--
			}
		case keyDown:
			if sel < len(rows) {
				sel++
			}
		}
	}
}

func orNotSet(s string) string {
	if strings.TrimSpace(s) == "" {
		return "not set · badge will be left to you"
	}
	return s
}

// screenStart runs the newcomer ladder behind a live checklist.
func screenStart(ctx context.Context, cfg engine.Config, o engine.StartOptions) error {
	if keys == nil {
		return cmdStart(ctx, cfg, o)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ctl := &engine.Control{}
	events := make(chan engine.StepEvent, 64)
	done := make(chan error, 1)

	v := &startView{start: time.Now(), dryRun: cfg.DryRun}
	v.draw()

	go func() {
		defer close(events)
		s, err := openSession(cfg)
		if err != nil {
			done <- err
			return
		}
		defer s.Close()
		o.Cfg, o.Ctl = cfg, ctl
		o.On = func(e engine.StepEvent) { events <- e }
		done <- s.GettingStarted(ctx, o)
	}()

	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()

	for {
		select {
		case e, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			v.apply(e)

		case err := <-done:
			for len(events) > 0 {
				v.apply(<-events)
			}
			v.done, v.err = true, err
			v.draw()
			key()
			return nil

		case b, ok := <-keys.ch:
			if !ok {
				return nil
			}
			switch b {
			case 's':
				ctl.Stop()
			case 'q', 3:
				ctl.Stop()
				cancel()
			}

		case <-tick.C:
		}
		v.draw()
	}
}

// cmdStart is the plain stdout version, for scripts and ssh.
func cmdStart(ctx context.Context, cfg engine.Config, o engine.StartOptions) error {
	s, err := openSession(cfg)
	if err != nil {
		return err
	}
	defer s.Close()

	o.Cfg = cfg
	o.On = func(e engine.StepEvent) {
		if e.Status == engine.StepRunning {
			fmt.Printf("  %s %s\n", style.Tint(style.Dim, "·"), e.Badge)
			return
		}
		m, c := mark(e.Status)
		fmt.Printf("  %s %s  %s\n", style.Tint(c, m), e.Badge, style.Tint(style.Dim, e.Note))
	}
	return s.GettingStarted(ctx, o)
}
