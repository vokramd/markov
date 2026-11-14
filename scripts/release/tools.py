#!/usr/bin/env python3
"""Install pinned release tools into an explicitly chosen user-local directory."""
import argparse
import hashlib
import io
import platform
import tarfile
import urllib.request
from pathlib import Path

TOOLS = {
    'goreleaser': ('goreleaser/goreleaser', 'v2.20.2', 'goreleaser_Linux_x86_64.tar.gz',
                  'f3d24969a7389a46613875711d15a96d8f6ef1553d84c6ace09d69ac1e7d44c0'),
    'syft': ('anchore/syft', 'v1.57.1', 'syft_1.57.1_linux_amd64.tar.gz',
             'bf52519eb57f675d47c07d43213cb6dff6449f3d6d1147ff6701ed91f44eef0c'),
}

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--directory', type=Path, required=True)
    args = parser.parse_args()
    if platform.system() != 'Linux' or platform.machine() not in ('x86_64', 'amd64'):
        parser.error('bootstrap checksums currently cover Linux amd64 only')
    args.directory.mkdir(parents=True, exist_ok=True)
    for name, (repo, version, asset, checksum) in TOOLS.items():
        url = f'https://github.com/{repo}/releases/download/{version}/{asset}'
        data = urllib.request.urlopen(url, timeout=120).read()
        if hashlib.sha256(data).hexdigest() != checksum:
            raise SystemExit(f'{name}: checksum mismatch')
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
            payload = archive.extractfile(name).read()
        target = args.directory / name
        target.write_bytes(payload)
        target.chmod(0o755)
        print(f'{name} {version}: verified {checksum}')

if __name__ == '__main__':
    main()
