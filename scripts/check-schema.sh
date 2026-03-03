#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

normalize() {
    python3 - "$1" <<'EOF'
import re, sys

text = open(sys.argv[1]).read()

start = re.search(r'CREATE TABLE IF NOT EXISTS', text)
if not start:
    print("")
    sys.exit(0)

lines = text[start.start():].splitlines()
out = []
in_table = False
for line in lines:
    stripped = line.strip()
    if not stripped or stripped.startswith("--"):
        continue
    if re.search(r'embedding\s+VECTOR', stripped, re.IGNORECASE):
        continue
    if re.search(r'\$\{embedding', stripped):
        continue

    norm = re.sub(r'IF NOT EXISTS\s+\S+\.memories', 'IF NOT EXISTS memories', stripped)
    norm = re.sub(r'\s+', ' ', norm).strip()

    if re.match(r'CREATE TABLE', norm):
        in_table = True
        out.append(norm)
        continue

    if not in_table:
        continue

    if re.match(r'(id|space_id|content|key_name|source|tags|metadata|version|updated_by|created_at|updated_at)\s', norm, re.IGNORECASE):
        out.append(norm)
    elif re.match(r'(UNIQUE\s+)?INDEX\s+', norm, re.IGNORECASE):
        idx_norm = re.sub(r',\s*$', '', norm)
        out.append(idx_norm)
    elif norm == ");":
        out.append(norm)
        break
    elif re.match(r'^\)', norm):
        out.append(");")
        break

print('\n'.join(out))
EOF
}

CANONICAL=$(mktemp)
normalize "$ROOT/direct-schema.sql" > "$CANONICAL"

fail=0

check() {
    local label="$1"
    local file="$2"
    local actual
    actual=$(normalize "$file")
    if ! diff -u "$CANONICAL" <(echo "$actual") > /dev/null 2>&1; then
        echo "FAIL: $label ($file) differs from direct-schema.sql"
        diff -u "$CANONICAL" <(echo "$actual") || true
        fail=1
    else
        echo "OK:   $label"
    fi
}

check "openclaw-plugin/schema.ts"              "$ROOT/openclaw-plugin/schema.ts"
check "opencode-plugin/src/direct-backend.ts"  "$ROOT/opencode-plugin/src/direct-backend.ts"
check "claude-plugin/hooks/common.sh"          "$ROOT/claude-plugin/hooks/common.sh"

rm -f "$CANONICAL"

if [[ $fail -ne 0 ]]; then
    echo ""
    echo "Schema drift detected. Update the diverging file(s) to match direct-schema.sql."
    exit 1
fi

echo ""
echo "All direct-mode schemas match direct-schema.sql."
