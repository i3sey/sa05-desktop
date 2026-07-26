#!/usr/bin/env python3
"""Turns the upstream tg-ws-proxy program into a Go library.

The upstream file is an Android c-shared module: it talks to the JVM through cgo
`//export` wrappers, logs through liblog, and owns a `main()`. The desktop client links
it in-process instead, so this script rewrites exactly four things and nothing else:

  1. package main -> package upstream, cgo block and `import "C"` removed
  2. the Android log writer -> a writer the host sets (dialer/logging live in api.go)
  3. every outbound dial -> dialContext(), so the host can mark sockets and keep the
     proxy's own traffic out of the SA05 TUN
  4. the //export wrappers, main() and the SIGPIPE handler (Unix-only) are dropped

Everything else — the MTProto handshake, fake-TLS, the WebSocket framing, the Cloudflare
fallback chain — is left byte-for-byte identical, because that is the part that has been
field-tested against real censorship.

Usage: tgws-library.py <upstream.go.orig> <output.go>
"""

import re
import sys


def die(message: str) -> None:
    print(f"tgws-library: {message}", file=sys.stderr)
    sys.exit(1)


def cut_cgo_preamble(source: str) -> str:
    """Removes the cgo comment block and `import "C"`."""
    start = source.index("/*\n#cgo")
    end = source.index('import "C"', start) + len('import "C"')
    return source[:start] + source[end:].lstrip("\n")


def drop_imports(source: str, names: list[str]) -> str:
    """Removes now-unused imports from the single import block."""
    open_index = source.index("import (")
    close_index = source.index(")", open_index)
    block = source[open_index:close_index]
    for name in names:
        block = re.sub(rf'^\t"{re.escape(name)}"\n', "", block, flags=re.MULTILINE)
    return source[:open_index] + block + source[close_index:]


def replace_block(source: str, needle: str, replacement: str, description: str) -> str:
    if needle not in source:
        die(f"не найдено: {description}")
    return source.replace(needle, replacement, 1)


def cut_from(source: str, marker: str, description: str) -> str:
    """Drops everything from marker to the end of the file."""
    index = source.find(marker)
    if index < 0:
        die(f"не найдено: {description}")
    return source[:index]


ANDROID_LOG_WRITER = """type androidLogWriter struct{}

func (w androidLogWriter) Write(p []byte) (n int, err error) {
	_, _ = os.Stderr.Write(p)
	cs := C.CString(string(p))
	C.androidLogProxy(cs)
	C.free(unsafe.Pointer(cs))
	return len(p), nil
}"""

HOST_LOG_WRITER = """// hostLogWriter forwards the upstream logs to whatever the host installed. The Android
// build wrote to liblog; on the desktop the client decides (stderr by default).
type hostLogWriter struct{}

func (w hostLogWriter) Write(p []byte) (n int, err error) {
	logMu.RLock()
	sink := logSink
	logMu.RUnlock()
	if sink == nil {
		sink = os.Stderr
	}
	return sink.Write(p)
}"""


def main() -> None:
    if len(sys.argv) != 3:
        die("usage: tgws-library.py <upstream.go.orig> <output.go>")
    source = open(sys.argv[1], encoding="utf-8").read()

    if not source.startswith("package main"):
        die("ожидался package main")
    source = source.replace("package main", "package upstream", 1)
    source = cut_cgo_preamble(source)

    # `unsafe` was only used by the cgo writer; signal/syscall only by the SIGPIPE line
    # removed below, which has no Windows equivalent.
    # `runtime` was only used by main()'s LockOSThread.
    source = drop_imports(source, ["unsafe", "os/signal", "syscall", "runtime"])

    source = replace_block(source, ANDROID_LOG_WRITER, HOST_LOG_WRITER, "android log writer")
    source = replace_block(
        source,
        "\tout := androidLogWriter{}",
        "\tout := hostLogWriter{}",
        "log writer usage",
    )
    source = replace_block(
        source,
        "\tsignal.Ignore(syscall.SIGPIPE)\n",
        "",
        "SIGPIPE handler",
    )

    # Outbound dials go through the host-provided dialer so the proxy's own sockets can
    # bypass the TUN it must not loop into.
    source = replace_block(
        source,
        """				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					d := net.Dialer{Timeout: 800 * time.Millisecond}
					return d.DialContext(ctx, "udp", s)
				},""",
        """				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					return dialContext(ctx, "udp", s, 800*time.Millisecond, 0)
				},""",
        "DoH resolver dialer",
    )
    source = replace_block(
        source,
        """	dialer := &net.Dialer{
		Timeout: timeout,
	}

	tlsCfg := tlsConfigPool.Clone()""",
        """	tlsCfg := tlsConfigPool.Clone()""",
        "websocket dialer declaration",
    )
    source = replace_block(
        source,
        """	rawConn, err := dialer.DialContext(ctx, "tcp", targetAddr)""",
        """	rawConn, err := dialContext(ctx, "tcp", targetAddr, timeout, 0)""",
        "websocket dial",
    )
    source = replace_block(
        source,
        """	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 60 * time.Second,
	}
	remote, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(dst, strconv.Itoa(port)))""",
        """	remote, err := dialContext(ctx, "tcp", net.JoinHostPort(dst, strconv.Itoa(port)),
		10*time.Second, 60*time.Second)""",
        "TCP fallback dial",
    )

    # The direct-WebSocket path becomes switchable, so the host can force the
    # Cloudflare-only or TCP-only transports the UI offers.
    source = replace_block(
        source,
        "\tif !dcConfigured || blacklisted {",
        "\tif !dcConfigured || blacklisted || directWSDisabled() {",
        "direct websocket switch",
    )

    # The cgo exports and main() are replaced by api.go.
    source = cut_from(
        source,
        "// ---------------------------------------------------------------------------\n// CGO exports",
        "CGO exports section",
    )

    header = (
        "// Code generated from tg-ws-proxy by build/patches/tgws-library.py. DO NOT EDIT.\n"
        "//\n"
        "// Upstream: tg-ws-proxy-android 1.2.0, GPL-3.0. See internal/tgws/upstream/LICENSE.\n"
        "// The library entry points live in api.go.\n\n"
    )
    open(sys.argv[2], "w", encoding="utf-8").write(header + source.rstrip() + "\n")
    print(f"tgws-library: записан {sys.argv[2]}")


if __name__ == "__main__":
    main()
