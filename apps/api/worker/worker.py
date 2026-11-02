"""DocIbis worker: converts documents with LibreOffice for the DocIbis server.

LibreOffice runs as a separate office process driven through UNO, the way
Docmosis drives it: LibreOfficeKit lays some anchored shapes out differently.
Requests come as JSON lines on stdin, replies go to stdout; the server
restarts the worker when it hangs or crashes. Run it with the Python bundled
with LibreOffice ($LO_PATH/python), which provides the uno module.

    request: {"id": 1, "in": "/tmp/a.odt", "out": "/tmp/a.pdf", "format": "pdf", "filter": "{...}"}
    replies: {"ready": true, "ms": 900} first, then {"id": 1, "ms": 150} or {"id": 1, "ms": 3, "error": "..."}
"""
import json
import os
import shutil
import signal
import subprocess
import sys
import tempfile
import time

import uno
from com.sun.star.beans import PropertyValue

FILTERS = {"pdf": "writer_pdf_Export", "docx": "MS Word 2007 XML", "odt": "writer8"}
# Export options in JSON, as LibreOffice writes them: {"SelectPdfVersion": {"type": "long", "value": "2"}}.
TYPES = {"long": int, "short": int, "unsigned long": int, "boolean": lambda v: str(v).lower() == "true", "string": str}
START_TIMEOUT = 60


def prop(name, value):
    p = PropertyValue()
    p.Name, p.Value = name, value
    return p


def filter_data(options):
    """{"SelectPdfVersion": {"type": "long", "value": "2"}} -> FilterData."""
    if not options:
        return ()
    data = []
    for name, spec in json.loads(options).items():
        convert = TYPES.get(spec.get("type"))
        if convert is None:
            raise ValueError(f"export option {name}: unknown type {spec.get('type')!r}")
        data.append(prop(name, convert(spec.get("value"))))
    return tuple(data)


def reply(out, **fields):
    out.write(json.dumps(fields) + "\n")
    out.flush()


# Expert settings of the profile: no image, text or object linked from outside
# the document is loaded (documents never come from a trusted location), no
# active content (OLE, DDE), no macro of any language.
SECURITY = """<?xml version="1.0" encoding="UTF-8"?>
<oor:items xmlns:oor="http://openoffice.org/2001/registry" xmlns:xs="http://www.w3.org/2001/XMLSchema" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="BlockUntrustedRefererLinks" oor:op="fuse"><value>true</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="DisableActiveContent" oor:op="fuse"><value>true</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="DisableMacrosExecution" oor:op="fuse"><value>true</value></prop></item>
<item oor:path="/org.openoffice.Office.Common/Security/Scripting"><prop oor:name="MacroSecurityLevel" oor:op="fuse"><value>3</value></prop></item>
</oor:items>
"""


def harden(profile):
    user = os.path.join(profile, "user")
    os.makedirs(user, exist_ok=True)
    with open(os.path.join(user, "registrymodifications.xcu"), "w", encoding="utf-8") as f:
        f.write(SECURITY)


class Office:
    def __init__(self, program, profile):
        harden(profile)
        self.pipe = f"docibis{os.getpid()}"
        # In the worker's process group: the server stops the whole group.
        self.process = subprocess.Popen(
            [os.path.join(program, "soffice"), "-env:UserInstallation=" + uno.systemPathToFileUrl(profile),
             f"--accept=pipe,name={self.pipe};urp;", "--headless", "--invisible", "--nologo", "--nodefault",
             "--nofirststartwizard", "--norestore", "--nolockcheck"],
            stdin=subprocess.DEVNULL, stdout=sys.stderr, stderr=sys.stderr)
        local = uno.getComponentContext()
        resolver = local.ServiceManager.createInstanceWithContext("com.sun.star.bridge.UnoUrlResolver", local)
        deadline = time.monotonic() + START_TIMEOUT
        while True:
            try:
                context = resolver.resolve(f"uno:pipe,name={self.pipe};urp;StarOffice.ComponentContext")
                break
            except Exception:
                if self.process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError("LibreOffice did not start")
                time.sleep(0.05)
        self.desktop = context.ServiceManager.createInstanceWithContext("com.sun.star.frame.Desktop", context)

    def convert(self, src, dst, fmt, options):
        name = FILTERS.get(fmt)
        if name is None:
            raise ValueError(f"unknown format {fmt!r}")
        export = [prop("FilterName", name), prop("Overwrite", True)]
        data = filter_data(options)
        if data:
            export.append(prop("FilterData", uno.Any("[]com.sun.star.beans.PropertyValue", data)))
        # Hidden, no macro, no link update: nothing runs or loads behind the template's back.
        doc = self.desktop.loadComponentFromURL(
            uno.systemPathToFileUrl(src), "_blank", 0,
            (prop("Hidden", True), prop("MacroExecutionMode", 0), prop("UpdateDocMode", 0)))
        if doc is None:
            raise RuntimeError(f"cannot load {os.path.basename(src)}")
        try:
            args = uno.Any("[]com.sun.star.beans.PropertyValue", tuple(export))
            uno.invoke(doc, "storeToURL", (uno.systemPathToFileUrl(dst), args))
        finally:
            doc.close(True)

    def alive(self):
        return self.process.poll() is None

    def usable(self):
        """False once LibreOffice died or its bridge is gone."""
        try:
            self.desktop.getFrames().getCount()
        except Exception:
            return False
        return self.alive()

    def stop(self):
        """Kills LibreOffice: every other process of the worker's group."""
        me, group = os.getpid(), os.getpgrp()
        if group != me:
            self.process.kill()
        else:
            for entry in os.listdir("/proc"):
                if entry.isdigit() and int(entry) != me:
                    try:
                        if os.getpgid(int(entry)) == group:
                            os.kill(int(entry), signal.SIGKILL)
                    except OSError:
                        pass
        self.process.wait()


def main():
    # Lead a process group, so that LibreOffice can be stopped with the worker.
    try:
        os.setpgid(0, 0)
    except OSError:
        pass
    out = sys.stdout
    program = os.environ.get("LO_PATH") or os.path.dirname(os.path.realpath(sys.executable))
    profile = tempfile.mkdtemp(prefix="docibis-lo-")
    office = None
    try:
        start = time.monotonic()
        try:
            office = Office(program, profile)
        except Exception as e:
            reply(out, ms=0, error=str(e))
            return 1
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
                office.convert(req["in"], req["out"], req["format"], req.get("filter", ""))
            except Exception as e:
                error = getattr(e, "Message", "") or str(e) or type(e).__name__
            fields = {"id": req.get("id", 0), "ms": int((time.monotonic() - t) * 1000)}
            if error:
                fields["error"] = error
            reply(out, **fields)
            if error and not office.usable():
                return 1  # the server starts another worker
        return 0
    finally:
        if office is not None:
            office.stop()
        shutil.rmtree(profile, ignore_errors=True)


if __name__ == "__main__":
    sys.exit(main())
