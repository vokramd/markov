#!/usr/bin/env python3
"""Collect dependency license/notice texts; missing licenses block public release review.

This is an inventory, not an automated legal determination. Review SPDX/SBOM too.
"""
import argparse
import hashlib
import json
import os
import subprocess
from pathlib import Path

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
decoder = json.JSONDecoder()
modules = {}
# Match all existing GoReleaser binary targets, not just the build host's imports.
for goos, goarch in [('linux', 'amd64'), ('linux', 'arm64'), ('darwin', 'amd64'), ('darwin', 'arm64')]:
    text = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/markov'],
                                   env=dict(os.environ, CGO_ENABLED='0', GOOS=goos, GOARCH=goarch), text=True)
    while text.strip():
        item, end = decoder.raw_decode(text.lstrip())
        text = text.lstrip()[end:]
        module = item.get('Module')
        if module and not module.get('Main'):
            modules[module['Path']] = module
records, notices, unresolved = [], [], []
for module in sorted(modules.values(), key=lambda m: m['Path']):
    effective = module.get('Replace', module)
    directory = Path(effective.get('Dir', '/nonexistent'))
    prefixes = ('LICENSE', 'LICENCE', 'COPYING', 'NOTICE', 'COPYRIGHT')
    files = sorted(f for f in directory.iterdir() if f.is_file() and f.name.upper().startswith(prefixes)) if directory.is_dir() else []
    identity = module['Path'] + '@' + module.get('Version', 'local')
    record = {'module': module['Path'], 'version': module.get('Version'),
              'replacement': effective.get('Path') if 'Replace' in module else None, 'files': []}
    if not files:
        unresolved.append(identity)
    for file in files:
        data = file.read_bytes()
        record['files'].append({'name': file.name, 'sha256': hashlib.sha256(data).hexdigest()})
        notices.append(f'=== {identity} / {file.name} ===\n' + data.decode('utf-8', errors='replace'))
    records.append(record)
a.output.mkdir(parents=True, exist_ok=True)
(a.output/'THIRD_PARTY_LICENSES.txt').write_text('\n\n'.join(notices)+'\n')
(a.output/'license-inventory.json').write_text(json.dumps({'modules': records, 'unresolved': unresolved}, indent=2)+'\n')
print(f'License inventory: {len(records)} modules, {len(unresolved)} require manual source/license resolution')
for item in unresolved:
    print('UNRESOLVED '+item)

if unresolved:
    raise SystemExit('Resolve missing dependency licenses before building candidate packages')
