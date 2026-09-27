# snoogrind

Grind Reddit achievements from one terminal UI. Drives a real browser through
Playwright and uses the session you are already logged into. No API token.

<p align="center"><img src="assets/screenshot.png" alt="snoogrind" width="860"></p>

- **share** · share → copy link on every post in your feed
- **scroll** · scrolls for hours and nothing else, for the banana badges
- **getting started** · the seven newcomer badges in one pass

## Install

```bash
git clone https://github.com/codeyevsky/snoogrind.git
cd snoogrind && go build -o bin/snoogrind .
install -Dm755 bin/snoogrind ~/.local/bin/snoogrind
```

Go 1.25+. State lives in `~/.local/share/snoogrind/`.

## Use

```bash
snoogrind
```

Pick **login** first, sign in by hand in the window that opens, then pick a
grinder. Arrows move, Enter selects, `q` quits. Inside a run, `p`
pauses and `s` stops.

`d` toggles **rehearsal mode**: every screen finds the controls it would press
and reports them without pressing anything. Reddit moves its markup around, so
check with this before turning something loose on your account.

## Notes

- **Headless does not work.** Reddit blocks it. Windowed by default.
- **A fresh profile gets a "prove your humanity" wall.** snoogrind waits 90
  seconds and it usually clears itself. Otherwise solve it once by hand, the
  profile is persistent.
- **Sharing has no daily cap**, so `shares per run = 1000` takes that whole
  ladder in one sitting. Already shared posts are skipped on later runs.
- **The banana count is calibrated from one hour, not from a real banana.**
  A measured session scrolled 2,290,137 px and moved Reddit's counter by about
  170 bananas, so snoogrind counts 13,470 px each (`pixels_per_banana` in
  `config.json`). That is roughly 170 an hour at the default pace, and it is
  snoogrind's own tally: it cannot read Reddit's counter. Whether scrolling
  faster earns proportionally more is untested.
- **Reddit words the newcomer badges as "via the Reddit app"** and snoogrind
  drives the web, so those seven may not all register. A profile banner and
  description only get set from text and an image you supply; verification
  needs a code, so that one stays yours.
- **If the markup changes**, runs start reporting `[?]`. Fix `findShareJS` and
  `findCopyJS` in `internal/engine/feed.go`.
