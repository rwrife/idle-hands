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
idle-hands watch --preset claude -- claude # tune detection for a known agent
idle-hands watch --json -- claude          # emit ndjson busy/idle events on stderr
idle-hands deck                            # see the available decks
idle-hands preset                          # see the agent presets
idle-hands stats                           # "reclaimed 14 min across 9 waits today"
idle-hands recap                           # today + this week + your streak 🔥
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
window is tallied to `~/.idle-hands/state.json` for `idle-hands stats`. Pass
`--preset <agent>` to start from detection tuning matched to a known agent (see
[Agent presets](#agent-presets)).

## Standalone watcher mode (GUI agents / no wrapping)

Some agents can't be run as a wrapped child — a GUI app, an IDE sidebar (Cursor,
VS Code + Copilot), or something you already have running. For those, point
`idle-hands` at a **running process by name** and it watches that process's CPU
activity instead of its output:

```bash
idle-hands watch --process code       # watch the running "code" process
idle-hands watch --process claude      # or a background CLI agent by name
```

It reuses the exact same BUSY/IDLE detector, decks, cards, quiet hours, and
stats as wrapped mode — only the *signal* differs. When the process goes quiet
(near-zero CPU: blocked, waiting on the model, "thinking") for the busy
threshold, one card fires; when it starts burning CPU again the card clears and
the reclaimed window is tallied. Stop it with Ctrl-C.

Name matching is case-insensitive against the process name (and, failing that,
its executable's basename). When several processes match (GUI apps spawn helper
trees), the busiest one is chosen so the target stays stable. `--process` and a
wrapped `-- <cmd>` are mutually exclusive.

**Platform caveats:**

- **Linux** — supported today via `/proc/<pid>/stat` CPU accounting. No extra
  permissions needed for your own processes.
- **macOS / Windows** — the CPU sampler isn't implemented yet, so this mode
  exits with a clear message there; wrapped mode (`watch -- <cmd>`) works on all
  platforms. Native samplers (macOS libproc, Windows PDH/Toolhelp) and an
  optional window-title/focus signal are tracked follow-ups.

## Plugin signals (drive busy/idle from any tool)

Wrapped mode and `--process` both *infer* busy/idle. But some agents know their
own state better than we can guess — an editor extension, a web UI, a CI runner,
a wrapper script. **Plugin signals** exposes a tiny, local-only endpoint so any
tool can POST authoritative `busy`/`idle` events and drive the exact same card
engine, quiet hours, and stats as the watchers.

Start a listener, then send it events:

```sh
idle-hands signal            # start the listener (Ctrl-C to stop)

# ...from anywhere else (a hook, an extension, a script):
idle-hands signal busy       # open a BUSY window → one card fires
idle-hands signal idle       # close it → reclaimed window recorded in stats
```

`busy` opens a BUSY window (subject to your configured threshold/quiet-hours
rules); `idle` closes it and records the reclaimed time. Duplicate `busy`/`idle`
events are **idempotent** — no double-count, no card flicker — so callers can
fire freely without tracking prior state.

**Wire protocol.** The listener speaks one JSON object per line over the socket:

```json
{"state":"busy","source":"vscode"}
{"state":"idle","source":"vscode"}
```

`source` is optional and used only for logging. A bare word (`busy` / `idle`)
is also accepted for shell one-liners. Wiring a custom agent is a one-liner on
each side of its work:

```sh
# In your agent wrapper: mark busy before the slow call, idle after.
idle-hands signal busy
my-agent --do-the-slow-thing
idle-hands signal idle

# Or talk to the socket directly (no idle-hands binary needed):
printf '{"state":"busy","source":"my-agent"}\n' | nc -U ~/.idle-hands/signal.sock
```

**Security — local only, no network.** On Linux/macOS the listener binds a
Unix domain socket at `~/.idle-hands/signal.sock` with `0600` permissions: it is
user-owned and never touches the network. A clean shutdown removes the socket
file, and a stale socket left by a crashed run is detected and replaced on the
next start. On **Windows** (no Unix sockets), it falls back to a loopback-only
TCP listener on `127.0.0.1`, publishing its chosen port to
`~/.idle-hands/signal.port`; this is still local-only by binding to loopback.

## JSON event stream (react to busy/idle from any tool)

Plugin signals let tools *push* state **in**. The `--json` flag is the mirror:
it lets tools *read* state **out**. Together they make `idle-hands` a small,
well-behaved local event hub.

Pass `--json` to `watch` (or set `json = true` in config) and idle-hands emits
one newline-delimited JSON object per state transition and per card show/dismiss,
so status bars, tmux, dashboards, or the plugin-signals consumers can react
without scraping the TUI:

```bash
idle-hands watch --json -- claude        # emit ndjson events on stderr
idle-hands watch --json -- aider 2>events.ndjson   # or capture them to a file
```

Events go to **stderr** by default so the wrapped agent's stdout/PTY stays
untouched. The stream is **off** unless you ask for it — default behavior is
unchanged. Each line is a compact JSON object with an RFC3339 UTC `ts`:

```json
{"ts":"2026-07-12T21:00:00Z","event":"state","state":"busy"}
{"ts":"2026-07-12T21:00:00Z","event":"card_shown","deck":"move","title":"Stand up & stretch"}
{"ts":"2026-07-12T21:00:42Z","event":"card_dismissed"}
{"ts":"2026-07-12T21:00:42Z","event":"state","state":"idle","reclaimed_seconds":42}
```

| event | fields | when |
| --- | --- | --- |
| `state` (`busy`) | `state` | output went quiet past the threshold |
| `card_shown` | `deck`, `title` | a card was drawn (fires again if the hook deck swaps in its real card) |
| `card_dismissed` | — | the on-screen card was cleared |
| `state` (`idle`) | `state`, `reclaimed_seconds` | the agent returned; window length in whole seconds |

During quiet hours or a focus block the `state` events still fire (the state
truly changed), but no `card_shown`/`card_dismissed` is emitted because no card
was drawn. A quick tmux status-bar consumer:

```bash
idle-hands watch --json -- claude 2>&1 >/dev/tty \
  | while read -r line; do
      state=$(printf '%s' "$line" | jq -r 'select(.event=="state").state // empty')
      [ -n "$state" ] && tmux set -g @agent "$state"
    done
```

The target descriptor can be changed with `--json-fd <n>` (or `json_fd` in
config); only `2` (stderr) is supported today — `1` (stdout) is rejected because
it would corrupt the wrapped agent's output.

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

Two more decks are generated live from *your* context rather than a fixed list:
**srs** (your own flashcards, one per wait — see
[Flashcards](#flashcards-spaced-repetition)) and **duckdiff** (one review
question about your staged `git diff` via a local LLM — see
[Duck the diff](#duck-the-diff-local-llm-review-question)). A third live deck,
**hook**, runs one of your own commands during the wait and shows its result
(see [Custom card hooks](#custom-card-hooks-do-real-work-during-the-wait)).

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

## Agent presets

Different agents "think" differently — Claude Code can reason for 20–30s behind
an `esc to interrupt` spinner, while a `gh copilot suggest` call is over in
seconds. Instead of hand-tuning `busy_threshold` and keyword hints per agent,
point `watch` at a **preset**:

```bash
idle-hands watch --preset claude -- claude
idle-hands watch --preset aider  -- aider
idle-hands watch --preset gh-copilot -- gh copilot suggest
```

Each preset pre-sets a busy threshold tuned for that agent and adds a few
agent-specific "thinking" keyword hints on top of the generic ones, so a chatty
spinner doesn't fool the detector. Shipped presets:

- **claude** — Claude Code (longer reasoning windows)
- **aider** — Aider (streams fairly promptly)
- **cursor** — Cursor agent
- **codex** — Codex CLI
- **gh-copilot** — `gh copilot suggest`/`explain` (short, snappy)

List them or inspect one before you commit:

```bash
$ idle-hands preset            # list every preset + its busy threshold
$ idle-hands preset claude     # show a preset's threshold and keyword hints
```

With **no** `--preset`, `idle-hands` uses its generic quiet-timeout detection
(unchanged). A preset is a convenience, not a requirement — and an explicit
`busy_threshold` in `~/.idle-hands/config.toml` always wins over a preset's
suggested threshold, so your config is never silently overridden. (Common
spellings like `claude-code`, `gh_copilot`, or `copilot` resolve to the right
preset.)

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

### Duck the diff (local LLM review question)

When you're waiting on the agent, the thing most worth thinking about is usually
**the change you just staged**. Point the built-in **duckdiff** deck at a local
[Ollama](https://ollama.com) model and each busy window shows one sharp
rubber-duck review question generated from your `git diff --cached`:

```
╭─ …agent is thinking (22s) ─────────────────────────╮
│  🦆  Duck the diff                                  │
│  You added a retry loop — what bounds the number    │
│  of attempts so it can't spin forever?              │
╰──────────────────────────────────── one card ──╯
```

It is built to **never get in your way**:

- No git repo, or nothing staged, simply falls back to the static **duck** deck
  (the generic rubber-duck prompts) — it's never an error.
- The model call is **time-boxed** (default `4s`). If Ollama is down, unreachable,
  or slow past the timeout, you get the static **duck** deck instead. The wait
  never blocks on the model, and neither does your agent.
- You get **one** question, not a wall of review notes.

Select it in config (works with zero extra keys if you already run Ollama):

```toml
deck = "duckdiff"
duckdiff_model   = "llama3.2"                          # any local Ollama model
duckdiff_url     = "http://localhost:11434/api/generate"  # Ollama's default endpoint
duckdiff_timeout = "4s"                                # fall back to duck past this
```

Stage something and preview exactly what you'd get with `idle-hands deck
duckdiff` (it shows the live question, or the fallback and why).

### Custom card hooks (do real work during the wait)

Every other deck asks *you* to do something. A **hook** instead runs one short
command you registered — `git fetch`, a single fast test, `go vet`, a lint pass
— the moment a busy window opens, and shows the result as the card. The wait
does real work instead of just reminding you to move.

```
╭─ …agent is thinking (24s) ─────────────────────────╮
│  🪝  ✅ fetch                                        │
│  ok · Fast-forwarded main to a1b2c3d               │
╰──────────────────────────────────── one card ──╯
```

Hooks are **strictly opt-in**: only commands you place in `[[hooks]]` ever run.
The command is an argv list run directly — nothing is passed through a shell,
word-split, or glob-expanded — so exactly the program you wrote is what runs.

It is built to **never get in your way**:

- Each run is bounded by **both** the busy window **and** a hard `hook_timeout`
  (default `10s`), whichever fires first. A hook that outlives the wait is
  killed.
- If the agent comes back before the hook finishes, the hook is **cancelled**
  and no card is shown — the window is just recorded as reclaimed time.
- The card shows the hook name, a ✅/❌ from the exit code, and the last line of
  output (truncated). A timeout renders a ❌ `timed out` card.
- With several hooks configured, idle-hands rotates through them round-robin so
  each gets a turn across successive waits.

Select it in config and register one or more hooks:

```toml
deck = "hook"
hook_timeout = "10s"     # hard ceiling per hook; also cancelled when the agent returns

[[hooks]]
name = "fetch"
cmd  = ["git", "fetch", "--quiet"]

[[hooks]]
name = "vet"
cmd  = ["go", "vet", "./..."]
```

> **Safety:** idle-hands only runs the exact commands you list here. It never
> infers a command, never runs anything through a shell, and never executes
> arbitrary output from the wrapped agent.

Preview the registered hooks with `idle-hands deck hook`.

## Config

All optional — with no config file you get the defaults above (the `move` deck,
a 20s threshold, no quiet hours). Drop a `~/.idle-hands/config.toml` to tune it;
changes take effect on the next run:

```toml
deck = "duck"            # which deck to show: move | duck | tidy | srs | duckdiff | hook | <your-deck>
busy_threshold = "30s"  # how long output must stay quiet before a card fires

[quiet_hours]           # suppress cards during these local hours (optional)
start = "22:00"         # cards are withheld 22:00 → 07:00; the agent is still
end   = "07:00"         # wrapped and reclaimed time is still tallied

[focus_safe]            # `idle-hands focus` behavior (optional)
suppress_stats = false  # true also excludes focus-block windows from stats

json = false            # emit the ndjson event stream (same as --json)
json_fd = 2             # descriptor for events; only 2 (stderr) is supported
```

Quiet-hours ranges may wrap past midnight (e.g. `22:00`→`07:00`). An unknown key
or a malformed value is reported loudly so a typo never silently does nothing.

The **srs** flashcard deck adds three optional keys — `srs_source` (path to your
card file), `srs_reveal` (question-only delay, default `6s`), and `srs_spacing`
(how many recent cards to hold back, default `3`). See
[Flashcards](#flashcards-spaced-repetition) above.

The **duckdiff** deck adds three optional keys — `duckdiff_model` (the Ollama
model, default `llama3.2`), `duckdiff_url` (the Ollama endpoint), and
`duckdiff_timeout` (how long to wait before falling back to the static duck
deck, default `4s`). See
[Duck the diff](#duck-the-diff-local-llm-review-question) above.

The **hook** deck adds `hook_timeout` (the hard per-hook ceiling, default `10s`)
and one or more `[[hooks]]` blocks — each with a `name` and an argv `cmd`. Only
the commands you list here ever run; see
[Custom card hooks](#custom-card-hooks-do-real-work-during-the-wait) above.

The **json** event stream adds two optional keys — `json` (enable the ndjson
stream, same as the `--json` flag) and `json_fd` (the target descriptor, default
`2`/stderr). See
[JSON event stream](#json-event-stream-react-to-busyidle-from-any-tool) above.

## Focus-safe mode

Even when the agent is busy, sometimes you're mid-thought and a card is an
interruption, not a gift. `idle-hands focus` hushes cards for a window you
choose while still detecting and (by default) counting the reclaimed time, so a
deep-focus block costs you the cards but not the scoreboard:

```bash
$ idle-hands focus 25m        # hush cards for 25 minutes
idle-hands 🎯 — focus-safe mode on for 25 min (until 14:05). Cards hushed; reclaimed time still counts.

$ idle-hands focus            # how much focus time is left?
idle-hands 🎯 — focus-safe mode on: 18 min left (until 14:05).

$ idle-hands focus off        # back to normal; cards show again
idle-hands 🎯 — focus-safe mode off; cards will show again.
```

The duration accepts any Go duration (`25m`, `1h30m`, `90s`). The focus-until
timestamp is stored in `~/.idle-hands/focus.json`, so a block survives restarts
and outlives the `watch` process — start focus in one terminal and every
`idle-hands watch` respects it until it expires or you clear it. A block set
mid-session takes effect on the next busy window without restarting `watch`.

By default focus suppresses only the on-screen card; reclaimed windows still
count toward `stats` and `recap`. Set `focus_safe.suppress_stats = true` to
exclude focus-block windows from the tally entirely.

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

## Recap (streak + weekly)

`stats` is today's number; **`recap`** is the payoff — it reads the same local
`state.json` (now a rolling multi-day history, kept 60 days by default) and
rolls it up into today's total, the last 7 days, and your current streak:

```bash
$ idle-hands recap
idle-hands 🙌 — reclaimed 22 min across 5 waits today.
This week: 1 h 48 min across 31 waits.
🔥 4-day streak.
```

A **streak** is the number of consecutive days — counting back from today —
that each reclaimed at least one window; a day with none breaks it (so keep
wrapping your agent to keep the fire lit). Add `--weekly` for a per-day
breakdown of the last 7 days, with gaps shown as a dash:

```bash
$ idle-hands recap --weekly
idle-hands 🙌 — reclaimed 22 min across 5 waits today.
This week: 1 h 48 min across 31 waits.
🔥 4-day streak.

Last 7 days:
  Thu 07-09  22 min across 5 waits
  Wed 07-08  31 min across 8 waits
  Tue 07-07  18 min across 6 waits
  Mon 07-06  37 min across 12 waits
  Sun 07-05  —
  Sat 07-04  —
  Fri 07-03  —
```

`stats` output for *today* is unchanged; `recap` just adds the longer view. An
existing single-day `state.json` from an older build is migrated to the history
format transparently on first read — no data loss, nothing to run.

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
goreleaser check                        # validate .goreleaser.yaml
goreleaser release --snapshot --clean   # artifacts land in ./dist
```

You don't have to remember to run that: CI's **release config + snapshot
build** job runs the exact same `check` + snapshot cross-compile on every PR
with the same pinned GoReleaser version the release uses, so a broken release
pipeline fails a PR long before anyone pushes a `v*` tag.

## License

MIT — see [`LICENSE`](./LICENSE).
