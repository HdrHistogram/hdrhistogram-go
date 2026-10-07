#!/usr/bin/env bash
# Minimises a native Go fuzzing corpus (the generated entries under
# $(go env GOCACHE)/fuzz/<module>/<Target>) by greedy set cover over statement
# blocks: every entry is replayed on its own with coverage enabled, and entries
# are kept, largest coverage first, only while they add a block not already
# covered. The fuzzer only ever adds entries, so without this the corpus keeps
# entries that later ones supersede.
#
# Block coverage is coarser than the fuzzer's edge coverage, so a minimised
# corpus may drop inputs the fuzzer would keep; it re-discovers them quickly,
# which is why CI runs this weekly rather than nightly. Entries written for an
# older signature of the target (which the fuzzer skips) are removed; entries
# that fail when replayed are always kept.
#
#   fuzz-corpus-minimize.sh <Target> <corpus-dir>
set -euo pipefail

target=${1:?usage: $0 <Target> <corpus-dir>}
corpus=${2:?usage: $0 <Target> <corpus-dir>}
cd "$(dirname "$0")/../.."

if [[ ! "$target" =~ ^Fuzz[A-Za-z0-9_]+$ ]]; then
	echo "invalid target name: $target" >&2
	exit 2
fi
if [ ! -d "$corpus" ] || [ -z "$(ls -A "$corpus")" ]; then
	echo "$target: empty corpus, nothing to minimise"
	exit 0
fi

work=$(mktemp -d)
seeds="testdata/fuzz/$target"
if [ -e "$seeds" ]; then
	# Go parses every testdata entry of a target even when -run selects one, so
	# replaying needs a testdata directory holding only the entry under test.
	echo "$seeds already exists; refusing to modify checked-in seeds" >&2
	exit 2
fi
cleanup() {
	rm -rf "$seeds" "$work"
	rmdir testdata/fuzz testdata 2>/dev/null || true
}
trap cleanup EXIT

go test -c -cover -covermode=set -coverpkg=. -o "$work/fuzz.test" .
mkdir -p "$work/cov" "$seeds"

# Replay every entry on its own, as the only seed of the target.
total=0
for path in "$corpus"/*; do
	name=$(basename "$path")
	total=$((total + 1))
	cp "$path" "$seeds/$name"
	if ! "$work/fuzz.test" -test.run="^${target}\$/^${name}\$" -test.count=1 \
		-test.coverprofile="$work/cov/$name" >"$work/out" 2>&1; then
		rm -f "$work/cov/$name"
		if grep -q 'in corpus entry' "$work/out"; then
			# Written for an older signature of the target: the fuzzer skips it.
			echo "remove (stale format): $name"
			rm -f "$path"
		else
			echo "keep (fails on replay): $name"
		fi
	fi
	rm -f "$seeds/$name"
done

python3 - "$corpus" "$work/cov" <<'PY'
import os, sys
corpus, covdir = sys.argv[1], sys.argv[2]
cover = {}
for name in os.listdir(covdir):
    blocks = set()
    with open(os.path.join(covdir, name)) as fh:
        for line in fh:
            if line.startswith("mode:"):
                continue
            pos, _, count = line.rsplit(" ", 2)
            if int(count) > 0:
                blocks.add(pos)
    cover[name] = blocks
seen, keep = set(), set()
for name in sorted(cover, key=lambda n: (-len(cover[n]), n)):
    if cover[name] - seen:
        keep.add(name)
        seen |= cover[name]
removed = [n for n in cover if n not in keep]
for n in removed:
    os.remove(os.path.join(corpus, n))
print(f"replayed {len(cover)}, kept {len(keep)} covering {len(seen)} blocks, removed {len(removed)}")
PY
echo "$target: $total entries before, $(find "$corpus" -type f | wc -l | tr -d ' ') after"
