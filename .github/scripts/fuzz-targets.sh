#!/usr/bin/env bash
# Lists the native Go fuzz targets (func FuzzXxx(f *testing.F)) in the module
# root, so CI matrices pick up new fuzzers without a hand-maintained list.
#
#   fuzz-targets.sh               one target per line
#   fuzz-targets.sh --json        a JSON array, for a GitHub Actions matrix
#   fuzz-targets.sh --check-cflite
#                                 fail if a target is not registered in
#                                 .clusterfuzzlite/build.sh, or if
#                                 ClusterFuzzLite cannot locate it
set -euo pipefail
cd "$(dirname "$0")/../.."

targets=$(grep -ho '^func Fuzz[A-Za-z0-9_]*(f \*testing\.F)' -- *_test.go |
	sed -e 's/^func //' -e 's/(.*//' | sort -u)
if [ -z "$targets" ]; then
	echo "no fuzz targets found" >&2
	exit 1
fi

case "${1:-}" in
"")
	echo "$targets"
	;;
--json)
	printf '%s\n' "$targets" | jq -R . | jq -cs .
	;;
--check-cflite)
	missing=0
	for t in $targets; do
		if ! grep -Eq "compile_native_go_fuzzer .* $t " .clusterfuzzlite/build.sh; then
			echo "::error file=.clusterfuzzlite/build.sh::fuzz target $t is not registered with compile_native_go_fuzzer"
			missing=1
		fi
		# compile_native_go_fuzzer locates a target by substring and needs exactly
		# one "func <name>" line mentioning testing.F; otherwise it prints "Could
		# not find the function" and silently skips the target. So no target name
		# may be a prefix of another function taking *testing.F.
		if [ "$(grep -rh --include='*.go' "func $t" . | grep -c 'testing.F')" -ne 1 ]; then
			echo "::error::fuzz target $t is ambiguous for ClusterFuzzLite: another testing.F function name starts with it; rename one of them"
			missing=1
		fi
	done
	exit $missing
	;;
*)
	echo "usage: $0 [--json | --check-cflite]" >&2
	exit 2
	;;
esac
