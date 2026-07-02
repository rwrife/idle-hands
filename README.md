# idle-hands 🙌

[![CI](https://github.com/rwrife/idle-hands/actions/workflows/ci.yml/badge.svg)](https://github.com/rwrife/idle-hands/actions/workflows/ci.yml)

> Idle hands are the devil's workshop. Yours are about to get a job.

A pocket-sized **break coach for the dead time while your AI coding agent thinks.**
Wrap your agent, and every time it goes quiet ("working…", "thinking…"),
`idle-hands` hands you **one** good 30-second micro-win — a stretch, a sip of
water, a single rubber-duck question, one tidy-up nudge — then vanishes the
instant the agent comes back.

Not a news feed. Not a productivity suite. **One card. One action. Auto-dismiss.**

```
$ idle-hands watch -- claude

  …agent is thinking (28s) ───────────────────────────
   🧍 Stand up. Roll your shoulders back 5×. Look at
      something 20 ft away for 20 seconds.
  ──────────────────────────────────────── one card ──

  👋 agent's back — where were we?
```

## Why

The wait while an AI coding agent churns has quietly become a recurring chunk of
the dev day. Most tools fill it with *more to read*. `idle-hands` does the
opposite: a closed-loop micro-action that finishes **before** the agent does and
returns your focus to the task.

## Status

**v0.1 — feature-complete.** Wrap your agent, get one card per think-window,
bring your own decks, tune it with config, and check your reclaimed time. See
[`PLAN.md`](./PLAN.md) for the full scope, milestones, and v0.2+ backlog.

## Install

Grab a prebuilt binary from the [latest release](https://github.com/rwrife/idle-hands/releases/latest)
(macOS / Linux / Windows, amd64 & arm64): download the archive for your
platform, extract `idle-hands`, and put it on your `PATH`.

Or with Go (1.23+):

```bash
go install github.com/rwrife/idle-hands/cmd/idle-hands@latest
```

Then wrap your agent and get on with it:

```bash
idle-hands watch -- <your-agent-command>   # e.g. claude, aider, codex
idle-hands deck                            # see the available decks
idle-hands stats                           # "reclaimed 14 min across 9 waits today"
```

## How it works

`watch` runs your command under a real pseudo-terminal and passes I/O straight
through — interactive agent TUIs render exactly as they would unwrapped, and the
exit code is preserved. It feeds the tapped output to the **BUSY/IDLE detector**:
when output goes quiet for the busy threshold (ignoring spinner/"thinking"
repaints so a chatty spinner can't fool it), `idle-hands` renders **exactly one
styled card** and clears it the instant real output resumes ("👋 agent's back —
reclaimed Ns"). One card per busy window, never the same one twice in a row.
Cards print on stderr so the agent's own output stays clean. The deck, busy
threshold, and quiet hours come from `~/.idle-hands/config.toml`; every reclaimed
window is tallied to `~/.idle-hands/state.json` for `idle-hands stats`.

## Build from source

Requires Go 1.23+.

```bash
git clone https://github.com/rwrife/idle-hands
cd idle-hands
go build ./cmd/idle-hands       # produces ./idle-hands
./idle-hands watch -- echo hi   # prints: hi
./idle-hands deck               # list available decks
./idle-hands stats              # reclaimed-time summary
./idle-hands version

go test ./...                   # run the test suite
```

To eyeball the wrapper, run the bundled noisy stand-in agent directly and then
under `watch` — they should look identical:

```bash
scripts/fake-agent.sh                          # bursts of output + quiet "thinking" gaps
./idle-hands watch -- scripts/fake-agent.sh    # same, through the wrapper
```

To watch the BUSY/IDLE detector fire a card, give the fake agent a "thinking"
gap longer than the 20s busy threshold (the spinner keeps repainting the whole
time — the detector treats it as noise and still flips to BUSY, then renders one
card):

```bash
# one ~23s think gap + spinner, then a work burst
ROUNDS=1 THINK=23 BURST=4 SPINNER=1 ./idle-hands watch -- bash scripts/fake-agent.sh
#   ╭─ …agent is thinking (20s) ──────────╮
#   │  🧍  Shoulder reset                   │
#   │  Stand up. Roll your shoulders back 5×. │
#   ╰─────────────────── one card ──╯
#   👋 agent's back — reclaimed 3s   (card clears the moment output resumes)

# short 3s gaps stay under the threshold → no card (no flapping)
ROUNDS=3 THINK=3 BURST=3 ./idle-hands watch -- bash scripts/fake-agent.sh
```

## Decks

On each busy window, `idle-hands` shows one card from a deck. Three ship
embedded in the binary (no config needed):

- **move** — stretch, posture, eyes, hydrate _(the default)_
- **duck** — one rubber-duck question about what you're building
- **tidy** — close one stray tab / triage one stale TODO

The same card is never shown twice in a row, and each busy window gets exactly
one. List what you've got, or preview a deck's cards before selecting it:

```bash
$ idle-hands deck            # list every deck (built-in + your own)
$ idle-hands deck duck       # preview every card in the duck deck
```

### Bring your own deck

Drop any number of `*.toml` files in `~/.idle-hands/decks/` and they show up
alongside the built-ins. A user deck whose `name` matches a built-in **overrides**
it (so you can replace `move` with your own stretches). The format is the same
one the built-ins use:

```toml
# ~/.idle-hands/decks/focus.toml
name = "focus"
description = "Tiny refocus prompts."
emoji = "🎯"

[[cards]]
title = "One thing"
text = "Name the single next action. Just one."

[[cards]]
title = "Why now?"
text = "Is this the most important thing, or just the loudest?"
```

Select it in config (`deck = "focus"`). A malformed deck file is reported loudly
rather than silently skipped, so a typo never just vanishes.

### Flashcards (spaced repetition)

Turn the wait into actual learning. Point the built-in **srs** deck at your own
flashcards and each busy window shows one card — the **question first**, then the
**answer** a beat later. Recently-shown cards are held back so you don't see the
same one twice in a few waits, and the reveal is purely timed: it never reads the
keyboard, so your agent keeps getting your keystrokes untouched.

Two card-source formats are supported, picked by file extension (with a content
sniff fallback):

- **Markdown Q/A** (`.md`) — blocks of `Q:` then `A:`, either can span multiple
  lines, and `---` separates cards:

  ```markdown
  Q: What does TCP stand for?
  A: Transmission Control Protocol.

  Q: Big-O of binary search?
  A: O(log n).
  ```

- **Anki text export** (`.txt`) — one note per line, `front<TAB>back`, exactly
  what Anki's "Notes in Plain Text" export produces (`#`-comment lines and simple
  HTML like `<b>`/`&nbsp;` are handled).

Select it and point at the file in config:

```toml
deck = "srs"
srs_source = "~/.idle-hands/cards.md"   # your Markdown Q/A or Anki .txt export
srs_reveal = "6s"                        # show the question this long, then the answer
srs_spacing = 3                          # deprioritize the last N cards (0 = only avoid repeats)
```

Preview exactly what you'll get with `idle-hands deck srs`.

## Config

All optional — with no config file you get the defaults above (the `move` deck,
a 20s threshold, no quiet hours). Drop a `~/.idle-hands/config.toml` to tune it;
changes take effect on the next run:

```toml
deck = "duck"            # which deck to show: move | duck | tidy | srs | <your-deck>
busy_threshold = "30s"  # how long output must stay quiet before a card fires

[quiet_hours]           # suppress cards during these local hours (optional)
start = "22:00"         # cards are withheld 22:00 → 07:00; the agent is still
end   = "07:00"         # wrapped and reclaimed time is still tallied
```

Quiet-hours ranges may wrap past midnight (e.g. `22:00`→`07:00`). An unknown key
or a malformed value is reported loudly so a typo never silently does nothing.

The **srs** flashcard deck adds three optional keys — `srs_source` (path to your
card file), `srs_reveal` (question-only delay, default `6s`), and `srs_spacing`
(how many recent cards to hold back, default `3`). See
[Flashcards](#flashcards-spaced-repetition) above.

## Stats

Every completed busy window is tallied to `~/.idle-hands/state.json` (a plain
JSON file — no DB, no telemetry, 100% local). `idle-hands stats` prints the
day's reclaimed time:

```bash
$ idle-hands stats
idle-hands 🙌 — reclaimed 14 min across 9 waits today.
All-time: 2 h 8 min across 71 waits.
```

(The all-time line appears once you have history beyond today.)

## Releasing

Releases are cut by [GoReleaser](https://goreleaser.com). Pushing a `v*` tag
triggers `.github/workflows/release.yml`, which cross-compiles binaries
(macOS / Linux / Windows, amd64 & arm64), builds archives + checksums, and
publishes a GitHub Release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

Dry-run the build locally without publishing (requires `goreleaser`):

```bash
goreleaser release --snapshot --clean   # artifacts land in ./dist
```

## License

MIT — see [`LICENSE`](./LICENSE).
