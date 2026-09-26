# snoogrind

Grind Reddit achievements from one terminal UI. It drives a real Chrome or
Firefox through Playwright and uses the session you are already logged into,
no password, no API token.

<p align="center"><img src="assets/screenshot.png" alt="snoogrind" width="860"></p>

Three of Reddit's badge ladders are farmable by repeating an action, and
snoogrind repeats them for you:

- **share** · walks your feed and does share → copy link on every post
- **scroll** · scrolls for hours and nothing else, for the banana distance
- **getting started** · the seven newcomer badges in one pass

Same family as [`liout`](https://github.com/codeyevsky/liout) and
[`githubFlex`](https://github.com/codeyevsky/ghFlex): one binary, one
full screen panel, hand rolled ANSI.

## Install

Requires Go 1.25+.

```bash
git clone https://github.com/codeyevsky/snoogrind.git
cd snoogrind
go build -o bin/snoogrind .
install -Dm755 bin/snoogrind ~/.local/bin/snoogrind
```

First launch downloads the Playwright browser once if you have no system
Chrome. State and settings live in `~/.local/share/snoogrind/`, or wherever
`$SNOOGRIND_HOME` points.

## Use

```bash
snoogrind
```

That is the whole interface. Pick **login** first · a browser window opens and
you sign in by hand · then whichever grinder you want.

| key | |
| --- | --- |
| arrows or `hjkl` | move between tiles |
| Enter | open the tile |
| `d` | rehearsal on/off |
| `q` | back, or quit from the menu |

**Rehearsal mode** (`d`) is worth knowing. Every screen then finds the controls
it would press and reports what it found, without pressing anything. Reddit
changes its markup often; this is how you check snoogrind still fits before you
let it loose on your account.

### The screens

- **Menu** · your account, your limits, and the six actions as tiles.
- **Share** · a live view while it walks the feed: elapsed clock, counters, a
  bar against the share limit, and each post scrolling past as it is handled.
  `p` pauses, `s` stops cleanly after the current post, `q` quits the run.
- **Scroll** · distance travelled, bananas, flicks, posts seen and reloads,
  with the same `p` / `s` / `q`.
- **Getting started** · a short setup (which subreddits, your profile text,
  your banner) and then a checklist that fills in live: `[✓]` earned, `[!]`
  yours to finish, `[x]` could not.
- **Settings** · four rows: shares per run, gap between posts, scroll session
  length, browser. Enter edits just the row you are on.
- **Login** · a spinner with the last status lines, so a slow browser step
  never looks like a hang.

## The achievements

### Share

| Badge | Shares |
| --- | --- |
| New Share | 1 |
| Sharing Enthusiast | 10 |
| Sharing Advocate | 50 |
| Sharing Pro | 100 |
| Sharing Legend | 1,000 |

No daily cap, so one sitting at `shares per run = 1000` takes the whole ladder.
The share screen shows which tier you are working towards.

### Banana (scroll distance)

| Badge | Banana lengths |
| --- | --- |
| Banana Baby | 10 |
| Banana Beginner | 100 |
| Banana Enthusiast | 1,000 |
| Banana Aficionado | 10,000 |
| Banana Master | 100,000 |
| Banana Legend | 500,000 |
| Potassium Overlord | 1,000,000 |

### Getting started

| Badge | What it takes | snoogrind |
| --- | --- | --- |
| Joined Reddit | have an account | already yours |
| Newcomer | join 1 subreddit | joins it |
| Person of Interests | join 5 subreddits | joins them |
| Detective Doggo | click 10 search results | searches and clicks |
| Feed Finder | change the feed type on home | flips what the web offers |
| Profile Perfectionist | banner + description | only with the text and image you give it |
| Secured Account | verify email or phone | yours · it needs a code |

A profile is yours: snoogrind fills in the words and the picture you hand it,
never ones it made up. Verification cannot be automated at all.

## How it works

**Share.** Every `shreddit-post` in the feed is read for its `id` (`t3_…`),
permalink, title and subreddit. Per post: scroll it into view, find its share
button, **real mouse click**, wait for the menu to load, click "Copy link",
Escape. The copy is confirmed by reading the clipboard back (`[✓]`); where the
clipboard is out of reach the on page "Copied" toast is the fallback (`[+]`).
Shared post ids go into `state.json`, so a second run skips them.

The action bar is **not** a child of `shreddit-post` · Reddit renders it as a
sibling · so each post is matched to its own
`shreddit-post-share-button[source-id="t3_…"]`, and the search walks open
shadow roots because the real `<button>` lives inside one.

**Scroll.** Each flick is a real wheel event over the middle of the feed
(320 to 900 px, 250 to 700 ms apart by default), and the distance comes from
`window.scrollY`, converted at the CSS definition of 96 px to the inch. When
the feed stops producing it waits for the next batch; if it stalls for good it
reopens the feed and carries on. Totals persist across sessions.

**Getting started.** Subreddit pages are littered with join buttons for sidebar
recommendations, so the join control is matched by the community it belongs to,
never by "the first one on the page".

## Things to know

- **Headless does not work.** Reddit answers headless Chrome with "blocked by
  network security". Windowed is the default for a reason.
- **A blank profile gets a human check.** The first launch may land on "prove
  your humanity"; snoogrind waits 90 seconds and it usually clears itself. If
  it does not, solve it once by hand · the profile is persistent, so it will
  not ask again. Alternative: point `profile_dir` at a browser profile you
  already use, with that browser closed.
- **Reddit words the newcomer badges as "via the Reddit app"**, and snoogrind
  drives the web. Sharing and scrolling count there; those seven may not all
  register.
- **The banana number is an estimate.** Reddit has never published how long its
  banana is. snoogrind takes 18 cm (`banana_cm` in `config.json`) and keeps its
  own count · it cannot read Reddit's counter. Treat it as a good guide, not as
  the badge's own progress.
- **Pace.** 50 shares and a 1.2 to 2.8 s gap by default; set anything up to 1,000.
  If Reddit says "you're doing that too much" the run stops itself, and so does
  a streak of five failures.
- **If Reddit changes its markup**, a run starts reporting `[?]` on every post.
  The selectors to fix are `findShareJS` / `findCopyJS` in
  `internal/engine/feed.go`; work with rehearsal mode on.

## Without a terminal

Every screen has a subcommand behind it, for scripts and ssh sessions with no
usable terminal. The interface is the intended way in; these are the escape
hatch.

```
snoogrind login     snoogrind share     snoogrind scroll
snoogrind badges    snoogrind install
```

| flag | |
| --- | --- |
| `--feed=home\|popular\|all\|best` | which feed to walk |
| `--url=https://…` | any Reddit page instead |
| `--max=50` | stop after this many shares, 1 to 1000 |
| `--scrolls=0` | stop after this many feed scrolls, `0` = until the feed dries up |
| `--delay="1200 2800"` | gap between posts, in ms |
| `--for=2h` | scroll only: how long to keep going, `0` = until stopped |
| `--subs=pics,books` | getting started: which subreddits to join |
| `--about=…` `--banner=…` | getting started: your profile text and image |
| `--browser=chrome\|firefox` | which browser to drive |
| `--profile-dir=~/.config/…` | drive a browser profile you already use |
| `--dry-run` | rehearsal, the TUI's `d` |

## Layout

```
main.go                  commands, screens, shared work
screen.go                frame painter, key reader, pager, spinner
ui.go                    banner, menu tiles, select, text field
runview.go               the live share screen
scrollview.go            the live scroll screen
startview.go             the getting started setup and checklist
settings.go              the settings list
internal/engine/
  browser.go             playwright session, persistent profile, login
  feed.go                post collection, share → copy link
  run.go                 the feed-walking loop
  scroll.go              the doomscroll loop
  start.go               the getting started ladder
  tiers.go               the badge thresholds
  config.go              settings (config.json)
  state.go               history (state.json)
internal/style/color.go  palette
```

## License

MIT.
