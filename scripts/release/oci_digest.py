#!/usr/bin/env python3
"""Resolve the actual two-platform OCI image index, not merely a layout reference."""
import hashlib
import json
from pathlib import Path
import re
import tarfile

INDEX_TYPES = {'application/vnd.oci.image.index.v1+json', 'application/vnd.docker.distribution.manifest.list.v2+json'}


def image_index_digest(path):
    with tarfile.open(path) as archive:
        def read(name):
            member = archive.getmember(name)
            if not member.isfile() or member.size > 16 * 1024**2:
                raise ValueError('invalid OCI index member')
            return archive.extractfile(member).read()
        raw = read('index.json')
        for _ in range(8):
            data = json.loads(raw)
            if data.get('mediaType') not in INDEX_TYPES | {None} or data.get('schemaVersion') != 2:
                raise ValueError('OCI image index required')
            descriptors = data.get('manifests', [])
            if len(descriptors) == 1 and descriptors[0].get('mediaType') in INDEX_TYPES:
                descriptor = descriptors[0]
                digest = descriptor.get('digest', '')
                if not re.fullmatch('sha256:[a-f0-9]{64}', digest):
                    raise ValueError('invalid index reference')
                raw = read('blobs/sha256/' + digest.split(':')[1])
                if 'sha256:' + hashlib.sha256(raw).hexdigest() != digest or len(raw) != descriptor.get('size'):
                    raise ValueError('index reference digest/size mismatch')
                continue
            platforms = {(d.get('platform', {}).get('os'), d.get('platform', {}).get('architecture')) for d in descriptors}
            if platforms != {('linux', 'amd64'), ('linux', 'arm64')} or len(descriptors) != 2:
                raise ValueError('exactly linux/amd64 and linux/arm64 required')
            for descriptor in descriptors:
                digest = descriptor.get('digest', '')
                if not re.fullmatch('sha256:[a-f0-9]{64}', digest):
                    raise ValueError('invalid platform manifest digest')
                manifest = read('blobs/sha256/' + digest.split(':')[1])
                if 'sha256:' + hashlib.sha256(manifest).hexdigest() != digest or len(manifest) != descriptor.get('size'):
                    raise ValueError('platform manifest digest/size mismatch')
            return 'sha256:' + hashlib.sha256(raw).hexdigest()
        raise ValueError('excessive index nesting')


def write_digest(path):
    digest = image_index_digest(path)
    Path(str(path) + '.digest').write_text(digest + '\n')
    return digest
