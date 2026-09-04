#!/usr/bin/env bash
# Build the reading and print forms of the PR/FAQ from the markdown.
#
# The markdown is the source. The HTML is generated so the two cannot drift,
# which matters here: every figure in the document is a measured fact, and a
# reviewer checking one against the deployment should not have to wonder which
# copy they are holding.
set -euo pipefail
cd "$(dirname "$0")"

python3 to-html.py > prfaq-wildtag.html
echo "wrote prfaq-wildtag.html"

if command -v google-chrome >/dev/null; then
  google-chrome --headless --disable-gpu --no-sandbox --no-pdf-header-footer \
    --virtual-time-budget=12000 \
    --print-to-pdf="WildTag-PRFAQ-v0.1.pdf" \
    "file://$PWD/prfaq-wildtag.html" >/dev/null 2>&1
  echo "wrote WildTag-PRFAQ-v0.1.pdf"

  # The template's limit: 1 page press release + up to 5 pages FAQ. Appendices
  # do not count. Fail loudly rather than quietly shipping a seven-page memo.
  if command -v pdftotext >/dev/null; then
    total=$(pdfinfo WildTag-PRFAQ-v0.1.pdf | awk '/^Pages/{print $2}')
    appendix=$(pdftotext -layout WildTag-PRFAQ-v0.1.pdf - | grep -c "Part 4: Appendix" || true)
    for p in $(seq 1 "$total"); do
      if pdftotext -f "$p" -l "$p" WildTag-PRFAQ-v0.1.pdf - 2>/dev/null | grep -q "Part 4: Appendix"; then
        body=$((p - 1)); break
      fi
    done
    echo "  press release + FAQ: ${body:-$total} pages (limit 6); appendix excluded; $total total"
    [ "${body:-$total}" -le 6 ] || { echo "  OVER THE TEMPLATE'S LIMIT"; exit 1; }
  fi
fi
