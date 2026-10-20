#!/usr/bin/env bash
# Tells a real fuzz failure from the fuzzing engine's process dying on its own.
#
#   fuzz-engine-death.sh TARGET FUZZ_LOG
#
# Exits 0 only if all of these hold, so the nightly can warn instead of reporting:
#   - the log's only failure is "fuzzing process hung or terminated unexpectedly[ while minimizing]: EOF";
#   - the log has no panic, fatal error, out-of-memory, data race or kill signal anywhere;
#   - the input the engine saved ("Failing input written to ...") replays cleanly on its own.
# This shows the input is not the cause. It cannot rule out a worker killed by memory growth over many inputs.
set -euo pipefail
cd "$(dirname "$0")/../.."
target=${1:?target}
log=${2:?fuzz log}

no() { echo "fuzz-engine-death: $1" >&2; exit 1; }

grep -q '^--- FAIL' "$log" || no "no failure block in the log"
grep -q -E '^[[:space:]]+fuzzing process hung or terminated unexpectedly( while minimizing)?: EOF$' "$log" || no "not an engine death"
if grep -q -E 'panic:|fatal error:|out of memory|DATA RACE|signal: ' "$log"; then no "panic, fatal error, memory or race message in the log"; fi
# Anything else under the failure block is a real message.
rest=$(grep -E -A12 -- '^--- FAIL' "$log" |
	grep -v -E -- '^--- FAIL|^[[:space:]]+(fuzzing process hung|Failing input written|To re-run:|go test )|^FAIL|^exit status|^--$|^[[:space:]]*$' || true)
[ -z "$rest" ] || no "other failure text: $(echo "$rest" | head -1)"

path=$(sed -n -E 's/^[[:space:]]+Failing input written to (testdata\/fuzz\/[A-Za-z0-9_]+\/[0-9a-f]+)$/\1/p' "$log" | head -1)
[ -n "$path" ] && [ -f "$path" ] || no "the engine saved no input"
[ "$(basename "$(dirname "$path")")" = "$target" ] || no "saved input is for another target"
name=$(basename "$path")
out=$(go test -v -count=1 -run="^${target}/${name}\$" . 2>&1) || { echo "$out" | tail -20 >&2; no "the saved input fails on replay"; }
echo "$out" | grep -q -- "--- PASS: ${target}/${name}" || no "the replay did not run the saved input"
