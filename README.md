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
idle-hands stats        # "reclaimed 14 min of spinner-staring today"
```

> **Today (M2):** `watch` runs your command under a real pseudo-terminal and
> passes I/O straight through — interactive agent TUIs render exactly as they
> would unwrapped, and the exit code is preserved. A copy of the output is
> already tapped internally; the BUSY/IDLE detector and cards land in M3–M4.

## Build from source

Requires Go 1.23+.

```bash
git clone https://github.com/rwrife/idle-hands
cd idle-hands
go build ./cmd/idle-hands       # produces ./idle-hands
./idle-hands watch -- echo hi   # prints: hi
./idle-hands version

go test ./...                   # run the test suite
```

To eyeball the wrapper, run the bundled noisy stand-in agent directly and then
under `watch` — they should look identical:

```bash
scripts/fake-agent.sh                          # bursts of output + quiet "thinking" gaps
./idle-hands watch -- scripts/fake-agent.sh    # same, through the wrapper
```

## Decks

- **move** — stretch, posture, eyes, hydrate
- **duck** — one rubber-duck question about what you're building
- **tidy** — close one stray tab / triage one stale TODO

Bring your own under `~/.idle-hands/decks/*.toml`.

## License

MIT
