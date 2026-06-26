#!/usr/bin/env bash
# fake-agent.sh — a noisy stand-in for an AI coding agent, used to eyeball the
# idle-hands wrapper (M2) and, later, to exercise the BUSY/IDLE detector (M3).
#
# It alternates between BURSTS of chatty output and QUIET gaps where it pretends
# to "think", optionally drawing a spinner. Run it directly or under the
# wrapper and confirm the two look identical:
#
#   scripts/fake-agent.sh
#   go run ./cmd/idle-hands watch -- scripts/fake-agent.sh
#
# Env knobs (all optional):
#   ROUNDS=4        number of think→work cycles
#   THINK=3         seconds of "thinking" (quiet gap) per round
#   BURST=8         lines of output per work burst
#   SPINNER=1       draw a spinner during the quiet gap (set 0 to go fully silent)
set -euo pipefail

ROUNDS="${ROUNDS:-4}"
THINK="${THINK:-3}"
BURST="${BURST:-8}"
SPINNER="${SPINNER:-1}"

frames='|/-\'

spin() {
	# Spin for $1 seconds, ~10 fps, rewriting a single line.
	local secs="$1" end i=0
	end=$(( $(date +%s) + secs ))
	while [ "$(date +%s)" -lt "$end" ]; do
		if [ "$SPINNER" = "1" ]; then
			local c="${frames:i++%4:1}"
			printf '\r  thinking %s ' "$c"
		fi
		sleep 0.1
	done
	if [ "$SPINNER" = "1" ]; then
		printf '\r                 \r'
	fi
	return 0
}

printf '🤖 fake-agent starting (rounds=%s think=%ss burst=%s)\n' "$ROUNDS" "$THINK" "$BURST"

for r in $(seq 1 "$ROUNDS"); do
	# --- quiet "thinking" gap: this is the idle window we want to reclaim ---
	spin "$THINK"

	# --- work burst: rapid-fire output, like tokens streaming in ---
	printf '── round %s/%s ─ working ──\n' "$r" "$ROUNDS"
	for l in $(seq 1 "$BURST"); do
		printf '  [%s.%s] doing a thing... %s\n' "$r" "$l" "$(date +%H:%M:%S)"
		sleep 0.05
	done
done

printf '✅ fake-agent done\n'
