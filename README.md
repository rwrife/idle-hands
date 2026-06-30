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

🚧 Early. See [`PLAN.md`](./PLAN.md) for scope, milestones, and backlog.

## Quick start (planned)

```bash
go install github.com/rwrife/idle-hands/cmd/idle-hands@latest
idle-hands watch -- <your-agent-command>
idle-hands stats        # "reclaimed 14 min across 9 waits today"
```

> **Today (M5):** `watch` runs your command under a real pseudo-terminal,
> passes I/O straight through (interactive agent TUIs render exactly as they
> would unwrapped, exit code preserved), and feeds the tapped output to the
> **BUSY/IDLE detector**. When output goes quiet for the busy threshold —
> ignoring spinner/"thinking" repaints so a chatty spinner can't fool it —
> `idle-hands` renders **exactly one styled card** and clears it the instant
> real output resumes ("👋 agent's back — reclaimed Ns"). One card per busy
> window, no repeats back-to-back. Cards print on stderr so the agent's own
> output stays clean. **New in M5:** the deck, busy threshold, and quiet hours
> are configurable via `~/.idle-hands/config.toml`, every reclaimed window is
> tallied to `~/.idle-hands/state.json`, and `idle-hands stats` reports your
> reclaimed time for the day.

## Build from source

Requires Go 1.23+.

```bash
git clone https://github.com/rwrife/idle-hands
cd idle-hands
go build ./cmd/idle-hands       # produces ./idle-hands
./idle-hands watch -- echo hi   # prints: hi
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
one. Pick a non-default deck in config (below). Bringing your own under
`~/.idle-hands/decks/*.toml` arrives in M6.

## Config

All optional — with no config file you get the defaults above (the `move` deck,
a 20s threshold, no quiet hours). Drop a `~/.idle-hands/config.toml` to tune it;
changes take effect on the next run:

```toml
deck = "duck"            # which deck to show: move | duck | tidy
busy_threshold = "30s"  # how long output must stay quiet before a card fires

[quiet_hours]           # suppress cards during these local hours (optional)
start = "22:00"         # cards are withheld 22:00 → 07:00; the agent is still
end   = "07:00"         # wrapped and reclaimed time is still tallied
```

Quiet-hours ranges may wrap past midnight (e.g. `22:00`→`07:00`). An unknown key
or a malformed value is reported loudly so a typo never silently does nothing.

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

## License

MIT
