"""Summarize every CSV in a directory into a markdown report."""

import csv
import os
import sys
from pathlib import Path

src, dest = Path(sys.argv[1]), Path(sys.argv[2])
lines = [f"# {os.environ.get('REPORT_TITLE', 'Report')}", ""]
for path in sorted(src.glob("*.csv")):
    with path.open() as f:
        rows = list(csv.DictReader(f))
    total = sum(int(r["count"]) for r in rows)
    lines.append(f"## {path.stem}: {total} items")
    lines += [f"- {r['name']}: {r['count']}" for r in rows]
    lines.append("")
dest.parent.mkdir(parents=True, exist_ok=True)
dest.write_text("\n".join(lines))
print(f"wrote {dest} from {len(list(src.glob('*.csv')))} files")
