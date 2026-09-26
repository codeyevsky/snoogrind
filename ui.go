// Full-screen widgets · banner, menu, single-select, text field, confirm.
// Everything here paints a whole frame and reads raw keys, so no screen ever
// scrolls away underneath the user.

package main

import (
	"regexp"
	"strings"
	"time"

	"github.com/codeyevsky/snoogrind/internal/style"
)

var banner = []string{
	"░█▀▀░█▀█░█▀█░█▀█░█▀▀░█▀▄░▀█▀░█▀█░█▀▄░",
	"░▀▀█░█░█░█░█░█░█░█░█░█▀▄░░█░░█░█░█░█░",
	"░▀▀▀░▀░▀░▀▀▀░▀▀▀░▀▀▀░▀░▀░▀▀▀░▀░▀░▀▀░░",
}

var bannerColors = []string{style.Ember, style.Orange, style.Rust}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z~]|\x1b.`)

type menuItem struct {
	cmd   string
	title string // what the tile says · falls back to cmd
	desc  string
	icon  []string // three rows of block art, six columns each
}

func (m menuItem) label() string {
	if m.title != "" {
		return m.title
	}
	return m.cmd
}

// addBanner draws the wordmark, optionally cut short for the reveal animation.
func addBanner(f *frame, upto int) {
	f.blank()
	for i, l := range banner {
		r := []rune(l)
		if upto >= 0 && upto < len(r) {
			r = r[:upto]
		}
		f.add(" " + style.Tint(bannerColors[i%len(bannerColors)], string(r)))
	}
	f.blank()
}

// revealBanner wipes the wordmark in from the left on the first screen.
func revealBanner(panel func(*frame, int)) {
	width := 0
	for _, l := range banner {
		if n := len([]rune(l)); n > width {
			width = n
		}
	}
	for k := 4; k < width; k += 4 {
		f := newFrame()
		panel(f, k)
		f.paint()
		time.Sleep(16 * time.Millisecond)
	}
}

// ---------- menu ----------

// padVis right-pads to w printable columns, counting what the eye sees rather
// than the SGR bytes woven through the string.
func padVis(s string, w int) string {
	if n := visLen(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return s
}

// wrap breaks text into lines of at most w columns, on word boundaries.
func wrap(s string, w int) []string {
	var out []string
	line := ""
	for _, word := range strings.Fields(s) {
		switch {
		case line == "":
			line = word
		case len([]rune(line))+1+len([]rune(word)) <= w:
			line += " " + word
		default:
			out = append(out, line)
			line = word
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// cardLines draws one menu tile · a bordered box with the action's name and
// what it does. The selected one gets a heavy orange frame so the eye lands on
// it without hunting for a highlight bar.
func cardLines(title, desc string, w, h int, sel bool) []string {
	tl, tr, bl, br, hz, vt := "┌", "┐", "└", "┘", "─", "│"
	frameColor, titleColor := style.Gray, style.Gray
	if sel {
		tl, tr, bl, br, hz, vt = "┏", "┓", "┗", "┛", "━", "┃"
		frameColor, titleColor = style.Orange, style.Orange+";1"
	}
	inner := w - 2
	edge := func(l, r string) string {
		return style.Tint(frameColor, l+strings.Repeat(hz, inner)+r)
	}
	row := func(content string) string {
		return style.Tint(frameColor, vt) + padVis(content, inner) + style.Tint(frameColor, vt)
	}

	// a blank under the title reads better, but not at the cost of cutting
	// the description short · it only goes in when the words leave room
	body := wrap(desc, inner-2)
	text := []string{style.Tint(titleColor, strings.ToUpper(title))}
	if len(body) <= h-4 {
		text = append(text, "")
	}
	for _, l := range body {
		text = append(text, style.Tint(style.Dim, l))
	}

	out := []string{edge(tl, tr)}
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(text) && text[i] != "" {
			line = " " + text[i]
		}
		out = append(out, row(line))
	}
	return append(out, edge(bl, br))
}

// zipCards lays a row of cards side by side into frame lines.
func zipCards(cards [][]string, gap int) []string {
	height := 0
	for _, c := range cards {
		if len(c) > height {
			height = len(c)
		}
	}
	out := make([]string, height)
	for i := 0; i < height; i++ {
		parts := make([]string, 0, len(cards))
		for _, c := range cards {
			if i < len(c) {
				parts = append(parts, c[i])
			}
		}
		out[i] = strings.Join(parts, strings.Repeat(" ", gap))
	}
	return out
}

// menuView is the home screen: wordmark, a short status panel, and the actions
// as tiles. Each row of the layout sizes its own cards, so a row holding one
// action gets a wide tile and a row of three gets narrow ones. An empty cmd
// with ok set means "d was pressed".
func menuView(layout [][]menuItem, panel func() []string, reveal bool) (string, bool) {
	row, col := 0, 0
	clamp := func() {
		if row < 0 {
			row = 0
		}
		if row >= len(layout) {
			row = len(layout) - 1
		}
		if col >= len(layout[row]) {
			col = len(layout[row]) - 1
		}
		if col < 0 {
			col = 0
		}
	}

	paint := func(f *frame, upto int) {
		addBanner(f, upto)
		for _, r := range panel() {
			f.add(" " + r)
		}
		f.rule("─", style.Rust)

		// share whatever is left between the panel and the footer, dropping
		// the breathing room between rows before the cards themselves shrink
		avail := f.h - len(f.lines) - 2
		vgap := 1
		if avail < len(layout)*6 {
			vgap = 0
		}
		cardH := (avail - vgap*(len(layout)-1)) / maxLen(len(layout), 1)
		if cardH < 4 {
			cardH = 4
		}
		if cardH > 7 {
			cardH = 7
		}

		// every tile is a third of the row, whatever the row holds · short
		// rows start at the left edge like everything else on the screen
		const hgap, cols = 2, 3
		cardW := (f.w - 2 - hgap*(cols-1)) / cols
		for r, items := range layout {
			var cards [][]string
			for c, it := range items {
				cards = append(cards, cardLines(it.label(), it.desc, cardW, cardH, r == row && c == col))
			}
			for _, l := range zipCards(cards, hgap) {
				f.add(l)
			}
			if r < len(layout)-1 && vgap > 0 {
				f.blank()
			}
		}
		footer(f, "  arrows or hjkl move · Enter select · d rehearse on/off · q quit")
	}

	if reveal {
		revealBanner(paint)
	}
	for {
		clamp()
		f := newFrame()
		paint(f, -1)
		f.paint()

		b, ok := key()
		if !ok {
			return "", false
		}
		switch b {
		case 'q', 3, 0x1b:
			return "", false
		case '\r', '\n':
			return layout[row][col].cmd, true
		case 'd':
			return "", true // the caller flips dry run and comes straight back
		case 'h':
			col--
		case 'l':
			col++
		case 'k':
			row--
		case 'j':
			row++
		}
	}
}

// ---------- single select ----------

// choose asks for one of options, drawn as a vertical list.
func choose(title, label string, options []string, def int) (string, bool) {
	sel := def
	if sel < 0 || sel >= len(options) {
		sel = 0
	}
	for {
		f := newFrame()
		header(f, title, "")
		f.blank()
		f.add("  " + style.Tint(style.Dim, label))
		f.blank()
		for i, o := range options {
			if i == sel {
				f.add("  " + style.Tint(style.Orange, "❯") + " " + style.Hl(o))
			} else {
				f.add("    " + style.Tint(style.Gray, o))
			}
		}
		footer(f, "  j/k move · Enter select · Esc cancel")
		f.paint()

		b, ok := key()
		if !ok {
			return options[sel], false
		}
		switch b {
		case '\r', '\n':
			return options[sel], true
		case 'q', 3, 0x1b:
			return options[sel], false
		case 'k':
			if sel > 0 {
				sel--
			}
		case 'j':
			if sel < len(options)-1 {
				sel++
			}
		}
	}
}

// ---------- text field ----------

// input edits one line of text in place. Enter keeps it, Esc keeps the default.
func input(title, label, def string) (string, bool) {
	val := []rune(def)
	for {
		f := newFrame()
		header(f, title, "")
		f.blank()
		f.add("  " + style.Tint(style.Dim, label))
		f.blank()
		f.add("  " + style.Tint(style.Orange, "❯ ") + string(val) + style.Tint(style.Orange, "▏"))
		footer(f, "  type to edit · Enter save · Esc cancel")
		f.paint()

		b, ok := key()
		if !ok {
			return def, false
		}
		switch {
		case b == '\r' || b == '\n':
			return strings.TrimSpace(string(val)), true
		case b == 3 || b == 0x1b:
			return def, false
		case b == 127 || b == 8:
			if len(val) > 0 {
				val = val[:len(val)-1]
			}
		case b == 21: // ctrl+u clears the field
			val = nil
		case b >= 32 && b < 127:
			val = append(val, rune(b))
		}
	}
}

// message shows a one-screen result and waits for a keypress.
func message(title string, lines ...string) {
	pager(title, lines)
}
