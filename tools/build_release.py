#!/usr/bin/env python3
"""Create a runtime-only archive from an explicit list of public files."""
import argparse
import gzip
import hashlib
import io
import json
from pathlib import Path
import tarfile

ROOT = Path(__file__).resolve().parents[1]
FILES = (
    'smartlight_trigger.py', 'agl_runtime.py', 'trigger_light.sh', 'rollback.sh',
    'carplay_monitor.py', 'carplay_light.sh', 'docs/carplay.md',
    'README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md',
    'docs/troubleshooting.md', 'docs/design.md', 'docs/testing.md',
    'vendor/SOURCES.json', 'vendor/LICENSE', 'vendor/urlparse.py',
    'vendor/json/__init__.py', 'vendor/json/decoder.py',
    'vendor/json/encoder.py', 'vendor/json/scanner.py',
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output', type=Path, default=ROOT / 'dist/smartlight-agl.tar.gz')
    args = parser.parse_args()
    for entry in json.loads((ROOT / 'vendor/SOURCES.json').read_text()):
        digest = hashlib.sha256((ROOT / 'vendor' / entry['path']).read_bytes()).hexdigest()
        if digest != entry['sha256']:
            raise SystemExit('Vendored file differs from manifest: ' + entry['path'])
    args.output.parent.mkdir(parents=True, exist_ok=True)
    with args.output.open('wb') as output:
        with gzip.GzipFile(filename='', mode='wb', fileobj=output, mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode='w') as archive:
                for name in FILES:
                    data = (ROOT / name).read_bytes()
                    member = tarfile.TarInfo('smartlight-agl/' + name)
                    member.size = len(data)
                    member.mode = 0o644
                    member.mtime = 0
                    archive.addfile(member, io.BytesIO(data))
    checksum = hashlib.sha256(args.output.read_bytes()).hexdigest()
    args.output.with_suffix(args.output.suffix + '.sha256').write_text(
        checksum + '  ' + args.output.name + '\n')
    print(str(args.output))
    print(checksum)


if __name__ == '__main__':
    main()
