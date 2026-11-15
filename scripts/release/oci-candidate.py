#!/usr/bin/env python3
"""Build an unsigned local multi-platform OCI archive, never push to a registry."""
import argparse
from oci_digest import write_digest
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

p = argparse.ArgumentParser(description=__doc__)
p.add_argument('--dist', type=Path, default=Path('dist'))
p.add_argument('--version', required=True)
p.add_argument('--output', type=Path, required=True)
a = p.parse_args()
repo = Path(__file__).resolve().parents[2]
engine = os.environ.get('CONTAINER_ENGINE', 'podman')
revision = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
a.output.parent.mkdir(parents=True, exist_ok=True)
with tempfile.TemporaryDirectory(prefix='cc71-oci-context-') as tmp:
    root = Path(tmp)
    for arch in ('amd64', 'arm64'):
        candidates = list(a.dist.glob(f'markov_linux_{arch}_*/markov'))
        if len(candidates) != 1:
            p.error(f'need exactly one prebuilt Linux {arch} binary')
        target = root / 'binaries' / arch / 'markov'
        target.parent.mkdir(parents=True)
        shutil.copyfile(candidates[0], target)
        target.chmod(0o755)
    shutil.copyfile(repo / 'LICENSE', root / 'LICENSE')
    shutil.copyfile(repo / '.release-licenses/THIRD_PARTY_LICENSES.txt', root / 'THIRD_PARTY_LICENSES.txt')
    shutil.copyfile(repo / 'examples/quickstart.yaml', root / 'quickstart.yaml')
    args = ['--platform=linux/amd64,linux/arm64', '--build-arg', f'VERSION={a.version}',
            '--build-arg', f'REVISION={revision}', '-f', str(repo / 'packaging/Containerfile.candidate')]
    if engine == 'docker':
        subprocess.run(['docker', 'buildx', 'build', *args, '--provenance=false', '--output', f'type=oci,dest={a.output.resolve()}', str(root)], check=True)
    elif engine == 'podman':
        name = 'localhost/cc71-markov-candidate:' + revision[:12]
        subprocess.run(['podman', 'build', *args, '--manifest', name, str(root)], check=True)
        # Podman resolves child images in its local manifest storage; skopeo may not.
        # Destination is a local filesystem archive, never docker:// or a registry.
        subprocess.run(['podman', 'manifest', 'push', '--all', '--format', 'oci',
                        '--digestfile', str(a.output.resolve()) + '.digest', name,
                        'oci-archive:' + str(a.output.resolve())], check=True)
    else:
        p.error('CONTAINER_ENGINE must be docker or podman')

# Both backends produce the same verifiable digest sidecar. No registry write.
write_digest(a.output.resolve())
