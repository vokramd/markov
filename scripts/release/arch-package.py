#!/usr/bin/env python3
"""Build one unsigned Arch package from the existing linux-amd64 binary archive."""
import argparse
import hashlib
import os
import re
import subprocess
import tempfile
from pathlib import Path

IMAGE = 'docker.io/library/archlinux:base-devel@sha256:fa2d4ce3310ddd90a4208e5168be8ad367da5129af1245f38fd2d803bcbe3239'
p = argparse.ArgumentParser(description=__doc__)
p.add_argument('archive', type=Path)
p.add_argument('--version', required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
if not re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+(?:[a-zA-Z0-9.+]*)?', a.version):
    p.error('use a pacman-compatible numeric version (map rc versions explicitly)')
repo = Path(__file__).resolve().parents[2]
a.output.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='markov-arch-') as tmp:
    root = Path(tmp)
    root.chmod(0o777)  # only disposable, non-secret package builder input/output
    data = a.archive.read_bytes()
    (root / 'markov.tar.gz').write_bytes(data)
    recipe = (repo / 'packaging/arch/PKGBUILD.in').read_text()
    recipe = recipe.replace('@VERSION@', a.version).replace('@SHA256@', hashlib.sha256(data).hexdigest())
    (root / 'PKGBUILD').write_text(recipe)
    engine = os.environ.get('CONTAINER_ENGINE', 'podman')
    mount = ['-v', f'{root}:/build:Z']
    # Rootless Podman maps container root to the caller; hosted Docker runs as real root.
    owner = '0:0' if Path(engine).name == 'podman' else f'{os.getuid()}:{os.getgid()}'
    try:
        subprocess.run([engine, 'run', '--rm', '--network=none', '--user', '65534:65534',
                        *mount, '-w', '/build', IMAGE, 'makepkg', '--nodeps', '--noconfirm'], check=True)
    finally:
        # Restore the caller ownership before Python cleans up the temporary tree.
        subprocess.run([engine, 'run', '--rm', '--network=none', *mount, IMAGE,
                        'chown', '-R', owner, '/build'], check=True)
    for package in root.glob('*.pkg.tar.zst'):
        (a.output / package.name).write_bytes(package.read_bytes())
    (a.output / 'PKGBUILD').write_text(recipe)
