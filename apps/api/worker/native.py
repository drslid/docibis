"""DocIbis native worker: makes PDFs with Typst for the DocIbis server.

A version of a template may have a Typst copy, written by hand: when it gives
the same document as LibreOffice, the server sends it here instead, about
thirty times faster. Same protocol as worker.py: JSON lines on stdin, replies
on stdout; the server restarts the worker when it hangs or crashes. Run it
with the Python bundled with LibreOffice, the typst package on PYTHONPATH.

    request: {"id": 1, "in": "/tmp/n/ab12/main.typ", "out": "/tmp/a.pdf", "format": "pdf",
              "inputs": {"data": "{...}", "today": "2026-11-03", "now": "2026-11-03T10:15:00Z"}}
    replies: {"ready": true, "ms": 40} first, then {"id": 1, "ms": 2} or {"id": 1, "ms": 1, "error": "..."}

The copy reads nothing but its own folder and the fonts: no package, no file
of the server, no network.
"""
import json
import os
import resource
import sys
import tempfile
import time

# No network for Typst packages (@preview/...), whatever the source asks.
for name in ("HTTPS_PROXY", "HTTP_PROXY", "ALL_PROXY", "https_proxy", "http_proxy", "all_proxy"):
    os.environ[name] = "http://127.0.0.1:9"
os.environ.pop("NO_PROXY", None)
os.environ.pop("no_proxy", None)

FONTS = [p for p in os.environ.get(
    "DOCIBIS_NATIVE_FONTS", "/opt/libreoffice26.8/share/fonts:/usr/share/fonts:/usr/local/share/fonts").split(":") if p]
MEMORY_MB = int(os.environ.get("DOCIBIS_NATIVE_MEMORY_MB", "1024"))
CACHED = 32


def reply(out, **fields):
    out.write(json.dumps(fields) + "\n")
    out.flush()


def main():
    out = sys.stdout
    start = time.monotonic()
    try:
        resource.setrlimit(resource.RLIMIT_AS, (MEMORY_MB << 20, MEMORY_MB << 20))
        import typst
        # Only the fonts LibreOffice uses, read once for every copy.
        fonts = typst.Fonts(include_system_fonts=False, include_embedded_fonts=False, font_paths=FONTS)
    except Exception as e:
        reply(out, ms=0, error=f"typst unavailable: {e}")
        return 1
    empty = tempfile.mkdtemp(prefix="docibis-typst-")
    compilers = {}

    def compiler(source):
        c = compilers.get(source)
        if c is None:
            if len(compilers) >= CACHED:
                compilers.clear()
            c = typst.Compiler(source, root=os.path.dirname(source), font_paths=fonts, ignore_system_fonts=True,
                               package_path=empty, package_cache_path=empty)
            compilers[source] = c
        return c

    reply(out, ready=True, ms=int((time.monotonic() - start) * 1000))
    for line in sys.stdin:
        try:
            req = json.loads(line)
        except ValueError:
            reply(out, ms=0, error="invalid request")
            continue
        t = time.monotonic()
        error = None
        try:
            if req.get("format", "pdf") != "pdf":
                raise ValueError("the native engine makes PDFs only")
            pdf, warnings = compiler(req["in"]).compile_with_warnings(
                format="pdf", sys_inputs=req.get("inputs") or {}, timestamp=int(time.time()))
            if warnings:
                # A warning (a font not found...) means a document that may differ.
                raise ValueError("warning: " + "; ".join(getattr(w, "message", str(w)) for w in warnings[:3]))
            with open(req["out"], "wb") as f:
                f.write(pdf)
        except Exception as e:
            error = (getattr(e, "message", "") or str(e) or type(e).__name__)[:1000]
        fields = {"id": req.get("id", 0), "ms": int((time.monotonic() - t) * 1000)}
        if error:
            fields["error"] = error
        reply(out, **fields)
    return 0


if __name__ == "__main__":
    sys.exit(main())
