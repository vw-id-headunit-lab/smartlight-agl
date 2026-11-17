# -*- coding: utf-8 -*-
"""Minimal Python 2.7/3 helpers; HTTP uses the installed curl, without a shell."""
from __future__ import print_function

import binascii
import os
import re
import signal

LIMIT = 2 * 1024 * 1024


def random_hex():
    return binascii.hexlify(os.urandom(16)).decode('ascii')


def temporary_parent():
    for path in (os.environ.get('TMPDIR'), '/tmp', '/var/tmp'):
        if path and os.path.isdir(path) and os.access(path, os.W_OK | os.X_OK):
            return path
    raise OSError('No writable temporary directory')


def unquote(value):
    # Only URL paths are passed here, for validation. Preserve non-ASCII text.
    return re.sub(r'%([0-9a-fA-F]{2})', lambda m: chr(int(m.group(1), 16)), value)


def config_quote(value):
    if any(c in value for c in ('\r', '\n', '\0')):
        raise RuntimeError('Invalid HTTP header')
    return value.replace('\\', '\\\\').replace('"', '\\"').replace('\t', '\\t')


def run_curl(argv):
    # Reap curl before the caller attempts queue cleanup after an interruption.
    pid = os.fork()
    if pid == 0:
        try:
            os.execv(argv[0], argv)
        except BaseException:
            os._exit(127)
    try:
        while True:
            try:
                unused, status = os.waitpid(pid, 0)
                break
            except OSError as exc:
                if exc.errno != 4:  # EINTR on Python 2 after a deferred signal
                    raise
    except BaseException:
        try:
            os.kill(pid, signal.SIGKILL)
        except OSError:
            pass
        os.waitpid(pid, 0)
        raise
    if os.WIFEXITED(status):
        return os.WEXITSTATUS(status)
    return -os.WTERMSIG(status)


def curl_request(method, url, headers, data, timeout):
    """Bound each request, disable proxies/redirects/retries, keep secrets off argv."""
    candidates = ['/usr/bin/curl'] + [os.path.join(p, 'curl')
                                     for p in os.environ.get('PATH', '').split(os.pathsep)]
    curl = next((p for p in candidates if os.path.isfile(p) and os.access(p, os.X_OK)), None)
    if not curl:
        raise RuntimeError('curl is required')
    folder = os.path.join(temporary_parent(), 'smartlight-http-' + random_hex())
    os.mkdir(folder, 0o700)
    paths = dict((name, os.path.join(folder, name)) for name in ('config', 'headers', 'body', 'response'))
    try:
        fd = os.open(paths['config'], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'wb') as output:
            for name, value in sorted(headers.items()):
                line = 'header = "' + config_quote(name + ': ' + value) + '"\n'
                output.write(line.encode('utf-8'))
        # --disable is first: never load a user's .curlrc (which could enable retries,
        # redirects, proxies, insecure TLS, or write response contents elsewhere).
        argv = [curl, '--disable', '--silent', '--noproxy', '*', '--globoff',
                '--proto', '=http,https', '--max-redirs', '0', '--max-filesize', str(LIMIT),
                '--connect-timeout', str(timeout), '--max-time', str(timeout),
                '--request', method, '--config', paths['config'],
                '--dump-header', paths['headers'], '--output', paths['response'], '--url', url]
        if data is not None:
            fd = os.open(paths['body'], os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
            with os.fdopen(fd, 'wb') as output:
                output.write(data)
            argv.extend(['--data-binary', '@' + paths['body']])
        status = run_curl(argv)
        if status:
            raise RuntimeError('curl exit %s' % status)
        code = None
        location = None
        with open(paths['headers'], 'rb') as source:
            raw_headers = source.read(65537)
        if len(raw_headers) > 65536:
            raise RuntimeError('Response headers exceed 64 KiB')
        for line in raw_headers.splitlines():
            if line.startswith(b'HTTP/'):
                code = int(line.split(b' ', 2)[1])
                location = None
            elif line.lower().startswith(b'location:'):
                location = line.split(b':', 1)[1].strip().decode('ascii')
        if code is None:
            raise RuntimeError('Missing HTTP status')
        with open(paths['response'], 'rb') as source:
            raw = source.read(LIMIT + 1)
        if len(raw) > LIMIT:
            raise RuntimeError('Response exceeds 2 MiB')
        return code, raw, location
    finally:
        for path in paths.values():
            if os.path.exists(path):
                os.unlink(path)
        os.rmdir(folder)
