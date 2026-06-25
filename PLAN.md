# idle-hands

> Idle hands are the devil's workshop. Yours are about to get a job.

## 1. Pitch

`idle-hands` is a tiny local daemon + TUI that notices when your AI coding agent (Claude Code, Aider, Cursor agent, Codex CLI, etc.) is **busy "thinking"** and hands you exactly **one** good thing to do with the 30-second-to-5-minute dead time: a stretch, a sip of water, a single flashcard from your own notes, a one-line rubber-duck prompt about the diff you're waiting on, or a nudge to triage one stale TODO. It is deliberately *not* a news feed and *not* a productivity suite — it gives you **one** card, then gets out of the way the instant your agent finishes.

## 2. Trend inspiration

Scanned Hacker News "Show HN", Reddit, and 2026 CLI/TUI roundups on 2026-06-25:

- **"Show HN: Turn Claude Code's 'thinking' wait into a dev news feed" (claudenews)** — https://github.com/bhpark1013/claudenews — multiple people are independently discovering that the *wait while an agent works* is now a recurring, unfilled chunk of a developer's day. This is the single freshest micro-niche I found.
- **"Show HN: The Agent Pantry – a live, daily-scanned landscape of AI agent tools"** — https://theagentpantry.com/ — confirms agent-adjacent tooling is where attention is in mid-2026.
- **"Show HN: Diplomat-agent — scan Python MCP servers for unguarded tool calls"** — https://github.com/Diplomat-ai/diplomat-agent — agent-observability is hot; people want lightweight local watchers around their agents.
- **"Show HN: A keyboard-centric clipboard history app for macOS" (ClipBook)** — https://clipbook.app/ — proof that small, single-purpose, keyboard-first desktop micro-tools still get real traction (141 pts, 116 comments). Validates the "do one tiny thing extremely well" shape.
- 2026 CLI roundups (toolshelf.dev/blog/best-cli-tools-2026, awesome-tuis) confirm the "terminal renaissance": single-purpose TUIs that compose are the current taste.

## 3. Why it's different

- **claudenews** fills the wait with *news to read* — passive, attention-grabbing, and (ironically) a great way to lose the thread of what you were doing. `idle-hands` does the opposite: it gives you a **closed-loop micro-action** that finishes *before* the agent does and returns your focus to the task. One card, one action, auto-dismiss.
- **Clipboard managers / Raycast / Alfred** are always-on launchers. `idle-hands` is *event-driven*: it only surfaces during detected agent-busy windows and is otherwise invisible. It's the negative space of those tools.
- **Pomodoro / break apps (Stretchly, etc.)** fire on fixed timers and interrupt you mid-flow. `idle-hands` is **flow-aware** — it triggers precisely when you're *already* blocked waiting on the agent, so the break costs you nothing.
- **Agent observability tools** (Diplomat-agent, dashboards) watch the agent for *the agent's* sake. We watch the agent purely to reclaim *your* idle seconds.

As far as I know, nothing today triggers a micro-break specifically on AI-agent "thinking" windows.

## 4. MVP scope (v0.1)

The smallest useful thing:

- A `idle-hands watch <command>` wrapper: runs your agent command, watches its stdout/stderr, and uses simple heuristics (output goes quiet for N seconds, or known "thinking/working/running" spinner strings) to flip between **BUSY** and **IDLE/you're-up** states.
- On entering a BUSY window longer than a threshold (default 20s), print **one** card to the terminal (or a small bottom panel): a single suggestion drawn from a built-in deck.
- Built-in decks: `move` (stretch/posture/eyes/water), `duck` (one rubber-duck question about the work), `tidy` (close one stray tab / one TODO).
- The card auto-clears the moment BUSY ends ("👋 agent's back — where were we?").
- A flat `~/.idle-hands/config.toml` to pick decks, set the busy threshold, and toggle quiet hours.
- `idle-hands stats` — count of idle windows reclaimed today + total seconds (a cheeky "you reclaimed 14 min of staring at a spinner").

## 5. Tech stack

- **Language: Go.** Single static binary, trivial cross-platform install (macOS/Linux/Windows), great at wrapping a subprocess and tee-ing its PTY. Boring, fast, no runtime.
- **PTY wrapping:** `github.com/creack/pty` — battle-tested, lets us pass the agent's TUI through untouched while we sniff output.
- **TUI/cards:** `charmbracelet/lipgloss` for clean card rendering (avoid full Bubble Tea complexity in v0.1; a rendered panel is enough).
- **Config:** TOML via `BurntSushi/toml` — human-editable, boring.
- **Stats store:** a plain JSON file in `~/.idle-hands/state.json`. No DB.

Justification: the whole tool is "wrap a process, detect quiet, draw a box." Go nails subprocess + cross-platform + single-binary distribution with zero ceremony.

## 6. Architecture

- `cmd/idle-hands` — CLI entrypoint, subcommands (`watch`, `stats`, `deck`, `version`).
- `internal/wrap` — PTY spawn + transparent passthrough + output tap channel.
- `internal/detect` — the BUSY/IDLE state machine (quiet-timeout + configurable spinner/keyword signals). This is the brain and the part we iterate most.
- `internal/deck` — deck loader; built-in decks embedded via `go:embed`, plus user decks from `~/.idle-hands/decks/*.toml`.
- `internal/card` — pick + render a card (lipgloss), handle auto-dismiss.
- `internal/store` — load/save daily stats JSON.
- `internal/config` — TOML load + defaults.

Data flow: `wrap` streams agent output → `detect` updates state → on BUSY-threshold, `card` asks `deck` for one card and renders it; on IDLE, `card` clears and `store` records the reclaimed window.

## 7. Milestones

1. **M1 — Scaffold + hello-world.** Go module, `cmd/idle-hands` with `version` and a stub `watch` that just execs the wrapped command transparently (no detection yet). CI builds the binary on all 3 OSes.
2. **M2 — Process wrapping + passthrough.** `internal/wrap` runs the child under a PTY, passes through I/O cleanly (agent TUIs render correctly), and exposes an output tap. Verified with a noisy fake "agent" script.
3. **M3 — BUSY/IDLE detector.** `internal/detect` state machine: quiet-for-N-seconds → BUSY, fresh output → IDLE. Emits state-change events. Threshold configurable.
4. **M4 — Card engine + built-in decks.** `internal/deck` + `internal/card`: on BUSY, render one card from embedded `move`/`duck`/`tidy` decks; clear on IDLE. lipgloss styling.
5. **M5 — Config + stats.** TOML config (decks, threshold, quiet hours) and `~/.idle-hands/state.json` with `idle-hands stats` summary ("reclaimed X min today").
6. **M6 — Polish + release.** README with GIF, `deck` subcommand to list/preview decks, user-deck loading from `~/.idle-hands/decks`, GoReleaser config + tagged v0.1 binaries.

## 8. Backlog / future features (v0.2+)

1. **Spaced-repetition deck** — point at a Markdown/Anki export; show one flashcard per idle window (turn waits into actual learning).
2. **Smart thresholds** — auto-learn your agent's typical think time and only fire on longer-than-usual stalls.
3. **Agent presets** — bundled detectors for Claude Code, Aider, Cursor CLI, Codex CLI, `gh copilot` so `--preset claude` just works.
4. **"Duck the diff"** — feed the staged `git diff` into a local Ollama model and surface one sharp review question as the card.
5. **Standalone watcher mode** — instead of wrapping, watch a window title / CPU of a named process so it works with GUI agents and IDE sidebars.
6. **Streak + daily recap** — "reclaimed 22 min across 31 waits today"; optional weekly summary.
7. **Hydration/posture goals** — opt-in nudges with gentle daily targets, snooze, and quiet hours.
8. **Custom card hooks** — let a card run a user script (e.g., `git fetch` in the background, run one fast test) so the wait does real work.
9. **Menu-bar / tray UI** — minimal native indicator showing BUSY/IDLE for people who don't live in one terminal.
10. **Team decks** — shareable deck files (onboarding tips, internal links) a team can drop in a repo.
11. **Focus-safe mode** — suppress cards during deep-focus blocks even if the agent is busy.
12. **Plugin signals** — let other tools POST "busy/idle" to a local socket so non-CLI agents can drive idle-hands.

## 9. Out of scope

- Any kind of news/article/social feed (that's the thing we're explicitly reacting against).
- Cloud sync, accounts, telemetry, or a backend — 100% local, single binary.
- Being a full Pomodoro/time-tracking suite or task manager.
- Controlling, steering, or modifying the AI agent itself — we only observe its output to detect busy/idle.
- Mobile apps.
- A general notification framework — it does idle-window cards, nothing else.
