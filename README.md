# idle-hands 🙌

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

## Decks

- **move** — stretch, posture, eyes, hydrate
- **duck** — one rubber-duck question about what you're building
- **tidy** — close one stray tab / triage one stale TODO

Bring your own under `~/.idle-hands/decks/*.toml`.

## License

MIT
