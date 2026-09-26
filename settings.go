// The settings screen · one browsable list instead of a march through every
// field. Enter edits the row you are on, nothing else asks you anything.

package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/codeyevsky/snoogrind/internal/engine"
	"github.com/codeyevsky/snoogrind/internal/style"
)

// setting is one editable row: how to show it, and what to do on Enter.
type setting struct {
	name string
	show func(engine.Config) string
	edit func(*engine.Config)
}

// numField edits one whole number in place.
func numField(name, prompt string, get func(engine.Config) int, set func(*engine.Config, int)) setting {
	return setting{
		name: name,
		show: func(c engine.Config) string { return strconv.Itoa(get(c)) },
		edit: func(c *engine.Config) {
			if v, ok := input("settings", prompt, strconv.Itoa(get(*c))); ok {
				set(c, atoiOr(v, get(*c)))
			}
		},
	}
}

// rangeField edits a min and max pair, which is how every pace setting is spelt.
func rangeField(name, unit, prompt string, get func(engine.Config) (int, int), set func(*engine.Config, int, int)) setting {
	return setting{
		name: name,
		show: func(c engine.Config) string {
			lo, hi := get(c)
			return fmt.Sprintf("%d to %d %s", lo, hi, unit)
		},
		edit: func(c *engine.Config) {
			lo, hi := get(*c)
			if v, ok := input("settings", prompt, fmt.Sprintf("%d %d", lo, hi)); ok {
				if a, b, ok := parseRange(v); ok {
					set(c, a, b)
				}
			}
		},
	}
}

// settingsList is deliberately short. These four are the knobs worth reaching
// for; the rest (scroll cap, flick size and gap, dry run, profile dir) live in
// config.json and on the command line, where they belong.
func settingsList() []setting {
	return []setting{
		numField("shares per run", fmt.Sprintf("stop after how many shares? (1 to %d)", engine.MaxSharesCap),
			func(c engine.Config) int { return c.MaxShares },
			func(c *engine.Config, n int) { c.MaxShares = n }),

		rangeField("gap between posts", "ms", "gap between posts in ms, two numbers",
			func(c engine.Config) (int, int) { return c.DelayMin, c.DelayMax },
			func(c *engine.Config, a, b int) { c.DelayMin, c.DelayMax = a, b }),

		{
			name: "scroll session",
			show: func(c engine.Config) string { return scrollForLabel(c) },
			edit: func(c *engine.Config) {
				if v, ok := input("settings", "how long should a scroll session run? (2h, 90m, 0 = until stopped)",
					firstNonEmpty(c.ScrollFor, "1h")); ok {
					c.ScrollFor = strings.TrimSpace(v)
				}
			},
		},

		{
			name: "browser",
			show: func(c engine.Config) string { return c.Browser },
			edit: func(c *engine.Config) {
				avail := engine.Available()
				if v, ok := choose("settings", "which browser to drive", avail, indexOf(avail, c.Browser)); ok {
					c.Browser = v
				}
			},
		},
	}
}

// screenSettings shows every setting at once and edits only what you pick.
func screenSettings(cfg engine.Config) error {
	items := settingsList()
	width := 0
	for _, it := range items {
		if n := len([]rune(it.name)); n > width {
			width = n
		}
	}

	sel := 0
	for {
		f := newFrame()
		header(f, "settings", engine.ConfigPath())
		f.blank()
		for i, it := range items {
			line := fmt.Sprintf("%-*s  %s", width, it.name, it.show(cfg))
			if i == sel {
				f.add("  " + style.Tint(style.Orange, "❯") + " " + style.Hl(line))
			} else {
				f.add("    " + style.Tint(style.Gray, line))
			}
		}
		footer(f, "  arrows move · Enter edit · q done")
		f.paint()

		b, ok := key()
		if !ok {
			return nil
		}
		switch b {
		case 'q', 3, 0x1b, '\t':
			return cfg.Save()
		case '\r', '\n', ' ':
			items[sel].edit(&cfg)
			cfg.Clamp()
			if err := cfg.Save(); err != nil {
				return err
			}
		case keyUp:
			if sel > 0 {
				sel--
			}
		case keyDown:
			if sel < len(items)-1 {
				sel++
			}
		}
	}
}
