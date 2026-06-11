#!/usr/bin/env bash
set -euo pipefail

# Add the Apache 2.0 boilerplate header to .go files that are missing it.

year=$(date +%Y)
for f in $(find . -name '*.go' -not -path './bin/*' -not -path './vendor/*'); do
    if ! head -2 "$f" | grep -q 'Copyright'; then
        echo "  FIXING: $f"
        header=$(sed "s/YEAR/$year/" hack/boilerplate.go.txt)
        printf '%s\n\n' "$header" | cat - "$f" > "$f.tmp" && mv "$f.tmp" "$f"
    fi
done
echo "Done."
