#!/usr/bin/env python3
"""Record unsigned candidate hashes and source identity, without publication."""
import hashlib
import json
import os
import subprocess
import sys
from pathlib import Path

root = Path(sys.argv[1]).resolve()
files = {}
for p in sorted(root.rglob('*')):
    if p.is_symlink():
        raise SystemExit(f'refuse symlink: {p}')
    if p.is_file() and p.name not in ('candidate.json', 'checksums.txt'):
        files[p.relative_to(root).as_posix()] = hashlib.sha256(p.read_bytes()).hexdigest()
manifest = {'schema': 1, 'signed': False,
            'commit': subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip(),
            'ref': os.environ.get('GITHUB_REF', 'local-working-tree'),
            'ci_run': os.environ.get('GITHUB_RUN_ID'),
            'working_tree_dirty': bool(subprocess.check_output(['git', 'status', '--porcelain'], text=True).strip()),
            'sha256': files}
(root / 'candidate.json').write_text(json.dumps(manifest, indent=2) + '\n')
lines = [f'{digest}  {path}' for path, digest in files.items()]
lines.append(f'{hashlib.sha256((root / "candidate.json").read_bytes()).hexdigest()}  candidate.json')
(root / 'checksums.txt').write_text('\n'.join(lines) + '\n')
