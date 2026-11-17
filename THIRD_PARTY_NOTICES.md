# Third-party notices / 第三方声明

Project-authored code is licensed under the [MIT License](LICENSE).

Go 联动运行包与独立 Python 手动调试包的第三方组件不同，以下分别说明。

## Go CarPlay runtime / Go 联动运行包

The Go application currently has no external Go module dependencies. Its static
binary includes the Go runtime and standard library, which are not covered by
this project's MIT license. Go is distributed under a BSD-style license:
Copyright 2009 The Go Authors.

The release builder copies the original license and patent notices from the
actual build toolchain into the archive:

- `licenses/go/LICENSE` and `licenses/go/PATENTS`: Go's license and patent grant.
- `licenses/go/src/.../LICENSE` and `PATENTS`, where present: additional notices
  found in standard-library dependency directories and their parent directories,
  including components vendored by Go. A notice's inclusion does not imply that
  every optional implementation described in it is linked into the binary.
- `licenses/go/SOURCES.json`: the Go version, target OS/architecture, original
  paths relative to GOROOT, and SHA-256 digests of the copied notice files.

These paths are generated inside the Go release archive, not stored as a fixed
copy in the source checkout. This keeps notices aligned with the toolchain used
by local builds and CI. Upstream reference: [Go license](https://go.dev/LICENSE).
The Go runtime package contains no Python runtime, CPython parser files, or
standalone Python trigger tool.

## Standalone Python debugging tool / 独立 Python 调试工具

The following files in the source checkout are copied without modification from
CPython 2.7.18 and remain part of the separate Python manual-trigger package:

- `python-trigger/vendor/json/__init__.py`
- `python-trigger/vendor/json/decoder.py`
- `python-trigger/vendor/json/encoder.py`
- `python-trigger/vendor/json/scanner.py`
- `python-trigger/vendor/urlparse.py`

Copyright (c) 2001–2020 Python Software Foundation; All Rights Reserved.

These files retain the terms and notices in `python-trigger/vendor/LICENSE`.
`python-trigger/vendor/SOURCES.json` records the exact upstream URLs and SHA-256
digests, including the original license. In the standalone Python package, these
files are `vendor/LICENSE` and `vendor/SOURCES.json`, relative to its extracted
`python-trigger/` directory; its own `THIRD_PARTY_NOTICES.md` provides local links.
Upstream reference: [CPython 2.7.18 license](https://github.com/python/cpython/blob/v2.7.18/LICENSE).

No changes were made to these CPython files. Only the standalone manual trigger
tool adds their directory to the module search path on Python 2, to supply
modules omitted by minimal embedded images. These source/Python-package paths
are not files in the Go archive and are not dependencies of the Go application.

## Product names and distribution / 产品名称与分发范围

This repository contains an independently written HTTP client and mock tests.
It does not distribute vehicle firmware, extracted service specifications,
controller binaries, or effect/movie assets. SmartLight, ID.Light, and other
product names belong to their respective owners. This is an independent
community project and is not an official vehicle manufacturer tool.
