#!/usr/bin/env bash
set -euo pipefail

fixture="$TEST_SRCDIR/$TEST_WORKSPACE/$1"
source_map="$TEST_SRCDIR/$TEST_WORKSPACE/$2"
value="$TEST_SRCDIR/$TEST_WORKSPACE/$3"

test -f "$fixture"
test -f "$source_map"
test -f "$value"
grep -F 'from "./value.js"' "$fixture"
grep -F 'sourceMappingURL=fixture.js.map' "$fixture"
grep -F 'tools/typescript/integration/fixture.ts' "$source_map"
grep -F '$/tools/typescript/integration/value.js' "$source_map"
