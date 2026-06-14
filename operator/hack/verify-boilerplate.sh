#!/usr/bin/env bash
set -euo pipefail

# Verify that all .go files contain the Apache 2.0 boilerplate header.

echo "Checking boilerplate headers..."
failed=0
for f in $(find . -name '*.go' -not -path './bin/*' -not -path './vendor/*'); do
    if ! head -2 "$f" | grep -q 'Copyright'; then
        echo "  MISSING: $f"
        failed=1
    fi
done

if [ "$failed" -eq 1 ]; then
    echo "ERROR: some files are missing the boilerplate header (see hack/boilerplate.go.txt)"
    exit 1
fi
echo "All files have boilerplate headers."
