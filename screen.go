// Full-screen drawing for the TUI · an alternate-screen frame buffer, a pager,
// a spinner for the slow browser steps, and the live run view. Hand-rolled
// ANSI, same as the rest of the family.

package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/codeyevsky/snoogrind/internal/style"
	"golang.org/x/term"
)

const (
	hideCursor = "\x1b[?25l"
	showCursor = "\x1b[?25h"
	altOn      = "\x1b[?1049h"
	altOff     = "\x1b[?1049l"
	home       = "\x1b[H"
	clearLine  = "\x1b[2K"
	clearBelow = "\x1b[J"
)

var altActive bool

func enterAlt() {
	if altActive {
		return
	}
	fmt.Print(altOn + home + clearBelow + hideCursor)
	altActive = true
}

func exitAlt() {
	if !altActive {
		return
	}
	fmt.Print(showCursor + altOff)
	altActive = false
}

// termSize returns the usable terminal box, with sane numbers when stdout is
// not a terminal (piped runs still render, just to a fixed width).
func termSize() (int, int) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 20 || h < 8 {
		return 80, 24
	}
	return w, h
}

// visLen counts printable columns, ignoring the SGR codes woven through a line.
func visLen(s string) int {
	return len([]rune(ansiRe.ReplaceAllString(s, "")))
}

// clip shortens a line to w visible columns, keeping colour codes intact and
// closing them off with a reset so the cut never bleeds into the next line.
func clip(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if visLen(s) <= w {
		return s
	}
	var b strings.Builder
	count := 0
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == 0x1b {
			// copy the whole escape sequence, it costs no columns
			j := i + 1
			for j < len(runes) && !isFinalByte(runes[j]) {
				j++
			}
			if j < len(runes) {
				j++
			}
			b.WriteString(string(runes[i:j]))
			i = j
			continue
		}
		if count == w-1 {
			b.WriteString("…")
			break
		}
		b.WriteRune(runes[i])
		count++
		i++
	}
	return b.String() + "\x1b[0m"
}

func isFinalByte(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '~'
}

// frame collects the lines of one screen, then paints them in a single pass so
// the terminal never shows a half-drawn view.
type frame struct {
	w, h  int
	lines []string
}

func newFrame() *frame {
	w, h := termSize()
	return &frame{w: w, h: h}
}

func (f *frame) add(s string)                 { f.lines = append(f.lines, s) }
func (f *frame) addf(format string, a ...any) { f.add(fmt.Sprintf(format, a...)) }
func (f *frame) blank()                       { f.add("") }

// rule draws a horizontal divider across the frame.
func (f *frame) rule(ch, color string) {
	f.add(style.Tint(color, strings.Repeat(ch, f.w-2)))
}

// pad fills the frame out to n lines so the footer always sits at the bottom.
func (f *frame) pad(n int) {
	for len(f.lines) < n {
		f.blank()
	}
}

func (f *frame) paint() {
	var b strings.Builder
	b.WriteString(home)
	for i, l := range f.lines {
		if i >= f.h {
			break
		}
		b.WriteString(clearLine)
		b.WriteString(clip(" "+l, f.w))
		if i < f.h-1 {
			b.WriteString("\r\n")
		}
	}
	b.WriteString(clearBelow)
	fmt.Print(b.String())
	os.Stdout.Sync()
}

// ---------- keyboard ----------

// keyReader turns stdin into a channel of key bytes. Arrow keys arrive as
// escape sequences; they are folded into the single letters the views use.
type keyReader struct {
	ch   chan byte
	stop chan struct{}
	fd   int
	old  *term.State
}

func newKeyReader() (*keyReader, error) {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	k := &keyReader{ch: make(chan byte, 64), stop: make(chan struct{}), fd: fd, old: old}
	go func() {
		buf := make([]byte, 32)
		send := func(b byte) bool {
			select {
			case k.ch <- b:
				return true
			case <-k.stop:
				return false
			}
		}
		for {
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(k.ch)
				return
			}
			for i := 0; i < n; {
				// A CSI sequence must be swallowed whole. Letting its bytes
				// through raw means left/right arrow reaches a view as a bare
				// ESC, which every one of them reads as "cancel".
				if buf[i] == 0x1b && i+1 < n && buf[i+1] == '[' {
					j := i + 2
					for j < n && (buf[j] < 0x40 || buf[j] > 0x7e) {
						j++
					}
					var final, param byte
					if j < n {
						final = buf[j]
					}
					if i+2 < n {
						param = buf[i+2]
					}
					switch {
					case final == 'A':
						if !send('k') {
							return
						}
					case final == 'B':
						if !send('j') {
							return
						}
					case final == 'C':
						if !send('l') {
							return
						}
					case final == 'D':
						if !send('h') {
							return
						}
					case final == '~' && param == '5':
						if !send('K') { // page up
							return
						}
					case final == '~' && param == '6':
						if !send('J') { // page down
							return
						}
					}
					// home, end and the rest: no meaning here
					i = j + 1
					continue
				}
				if !send(buf[i]) {
					return
				}
				i++
			}
		}
	}()
	return k, nil
}

func (k *keyReader) close() {
	if k == nil {
		return
	}
	close(k.stop)
	_ = term.Restore(k.fd, k.old)
}

// keys is the one reader the whole TUI session shares. Per-screen readers do
// not work: closing one leaves its goroutine parked inside os.Stdin.Read, and
// that goroutine then swallows the first key meant for the next screen.
var keys *keyReader

// startKeys puts the terminal in raw mode for the session. A failure means
// there is no usable terminal, and the caller should fall back to plain output.
func startKeys() error {
	if keys != nil {
		return nil
	}
	k, err := newKeyReader()
	if err != nil {
		return err
	}
	keys = k
	return nil
}

func stopKeys() {
	if keys != nil {
		keys.close()
		keys = nil
	}
}

// key blocks for the next keypress. The second result is false once stdin is
// gone, which every view treats as "leave".
func key() (byte, bool) {
	if keys == nil {
		return 0, false
	}
	b, ok := <-keys.ch
	return b, ok
}

// ---------- shared chrome ----------

// header draws the title bar every screen shares.
func header(f *frame, title, right string) {
	left := style.Tint(style.Orange+";1", "snoogrind") + style.Tint(style.Dim, "  ·  ") + title
	gap := f.w - 2 - visLen(left) - visLen(right)
	if gap < 1 {
		gap = 1
	}
	f.add(left + strings.Repeat(" ", gap) + style.Tint(style.Dim, right))
	f.rule("─", style.Rust)
}

// footer draws the key hints, pinned to the bottom of the frame.
func footer(f *frame, keys string) {
	f.pad(f.h - 2)
	f.rule("─", style.Rust)
	f.add(style.Tint(style.Dim, keys))
}

// ---------- pager ----------

// pager shows a list of lines full-screen with j/k scrolling · used wherever a
// screen's output can run past the bottom of the terminal.
func pager(title string, lines []string) {
	if keys == nil {
		for _, l := range lines {
			fmt.Println(" " + l)
		}
		return
	}
	top := 0
	for {
		f := newFrame()
		body := f.h - 4 // header (2) + footer (2)
		if body < 1 {
			body = 1
		}
		maxTop := len(lines) - body
		if maxTop < 0 {
			maxTop = 0
		}
		if top > maxTop {
			top = maxTop
		}
		if top < 0 {
			top = 0
		}

		pos := "all"
		if len(lines) > body {
			pos = fmt.Sprintf("%d–%d of %d", top+1, min(top+body, len(lines)), len(lines))
		}
		header(f, title, pos)
		for i := top; i < len(lines) && i < top+body; i++ {
			f.add(lines[i])
		}
		footer(f, "  j/k scroll · Enter or q back")
		f.paint()

		b, ok := key()
		if !ok {
			return
		}
		switch b {
		case 'q', '\r', '\n', 3, 0x1b:
			return
		case 'j':
			top++
		case 'k':
			top--
		case 'J', ' ':
			top += body
		case 'K':
			top -= body
		case 'g':
			top = 0
		case 'G':
			top = maxTop
		}
	}
}

// ---------- spinner ----------

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// busy runs fn in the background and holds a live status screen in front of it
// · the browser steps take tens of seconds, and a frozen terminal reads as a
// hang. fn reports what it is doing through the emit callback it is handed.
func busy(title, hint string, fn func(emit func(string)) error) error {
	status := make(chan string, 64)
	done := make(chan error, 1)
	go func() {
		done <- fn(func(msg string) {
			select {
			case status <- msg:
			default:
			}
		})
	}()

	var log []string
	tick := time.NewTicker(90 * time.Millisecond)
	defer tick.Stop()
	start := time.Now()
	i := 0

	for {
		select {
		case msg := <-status:
			log = append(log, msg)
			if len(log) > 12 {
				log = log[len(log)-12:]
			}
		case err := <-done:
			return err
		case <-tick.C:
			i++
		}

		f := newFrame()
		header(f, title, time.Since(start).Truncate(time.Second).String())
		f.blank()
		f.add("  " + style.Tint(style.Orange, spinnerFrames[i%len(spinnerFrames)]) + "  " + lastOr(log, "starting…"))
		f.blank()
		for _, l := range log[:maxLen(len(log)-1, 0)] {
			f.add("     " + style.Tint(style.Dim, l))
		}
		footer(f, "  "+hint)
		f.paint()
	}
}

func lastOr(list []string, def string) string {
	if len(list) == 0 {
		return def
	}
	return list[len(list)-1]
}

func maxLen(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------- progress bar ----------

// bar renders a proportional meter, e.g. ███████░░░░░░░░ 12/50. Width comes
// from the frame, so a narrow terminal must not turn into a negative repeat.
func bar(done, total, width int) string {
	if width < 1 {
		width = 1
	}
	if total <= 0 {
		total = 1
	}
	if done > total {
		done = total
	}
	if done < 0 {
		done = 0
	}
	filled := done * width / total
	return style.Tint(style.Orange, strings.Repeat("█", filled)) +
		style.Tint(style.Dim, strings.Repeat("░", width-filled))
}
