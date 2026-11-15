# Third-party notices

Project-authored code is licensed under the [MIT License](LICENSE).

The following files are copied without modification from CPython 2.7.18:

- `vendor/json/__init__.py`
- `vendor/json/decoder.py`
- `vendor/json/encoder.py`
- `vendor/json/scanner.py`
- `vendor/urlparse.py`

Copyright (c) 2001–2020 Python Software Foundation; All Rights Reserved.

These files retain the terms and notices in [vendor/LICENSE](vendor/LICENSE).
[vendor/SOURCES.json](vendor/SOURCES.json) records the exact upstream URLs and
SHA-256 digests, including the original license. No changes were made to these
CPython files. The application adds their directory to the module search path
only on Python 2, to supply modules omitted by minimal embedded images.

This repository contains an independently written HTTP client and mock tests.
It does not distribute vehicle firmware, extracted service specifications,
controller binaries, or effect/movie assets. SmartLight, ID.Light, and other
product names belong to their respective owners. This is an independent
community project and is not an official vehicle manufacturer tool.
