import hashlib
import io
import json
from pathlib import Path
import tarfile
import tempfile
import unittest
from oci_digest import image_index_digest, write_digest


class OCIDigestTests(unittest.TestCase):
    def archive(self, nested=False, corrupt=False, one_arch=False, media_type=True):
        self.temp = tempfile.TemporaryDirectory(prefix='cc71-oci-digest-')
        self.addCleanup(self.temp.cleanup)
        path = Path(self.temp.name) / 'image.tar'
        blobs = {}
        descriptors = []
        for arch in ('amd64',) if one_arch else ('amd64', 'arm64'):
            raw = json.dumps({'schemaVersion': 2, 'fixture_arch': arch}).encode()
            digest = hashlib.sha256(raw).hexdigest()
            blobs['blobs/sha256/' + digest] = raw
            descriptors.append({'mediaType': 'application/vnd.oci.image.manifest.v1+json',
                                'digest': 'sha256:' + digest, 'size': len(raw),
                                'platform': {'os': 'linux', 'architecture': arch}})
        index = {'schemaVersion': 2, 'manifests': descriptors}
        if media_type:
            index['mediaType'] = 'application/vnd.oci.image.index.v1+json'
        raw = json.dumps(index).encode()
        digest = hashlib.sha256(raw).hexdigest()
        if nested:
            blobs['blobs/sha256/' + digest] = raw + (b'corruption' if corrupt else b'')
            raw = json.dumps({'schemaVersion': 2, 'manifests': [{'mediaType': 'application/vnd.oci.image.index.v1+json',
                              'digest': 'sha256:' + digest, 'size': len(raw)}]}).encode()
        blobs['index.json'] = raw
        with tarfile.open(path, 'w') as tar:
            for name, value in blobs.items():
                member = tarfile.TarInfo(name); member.size = len(value)
                tar.addfile(member, io.BytesIO(value))
        return path, 'sha256:' + digest

    def test_flat_buildx_layout(self):
        path, digest = self.archive()
        self.assertEqual(write_digest(path), digest)
        self.assertEqual(Path(str(path) + '.digest').read_text(), digest + '\n')

    def test_typeless_flat_layout(self):
        path, digest = self.archive(media_type=False)
        self.assertEqual(image_index_digest(path), digest)

    def test_nested_podman_layout(self):
        path, digest = self.archive(nested=True)
        self.assertEqual(image_index_digest(path), digest)

    def test_nested_blob_hash_tamper(self):
        path, _ = self.archive(nested=True, corrupt=True)
        with self.assertRaisesRegex(ValueError, 'mismatch'):
            image_index_digest(path)

    def test_missing_target_architecture(self):
        path, _ = self.archive(one_arch=True)
        with self.assertRaisesRegex(ValueError, 'amd64'):
            image_index_digest(path)

if __name__ == '__main__':
    unittest.main()
