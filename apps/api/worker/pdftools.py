"""DocIbis PDF tools: the pages of a PDF drawn in grey or read as text, with
PDFium (the PDF engine of Chrome, pypdfium2 package) in place of poppler.
The server compares PDFs with them. Run with the Python bundled with
LibreOffice, pypdfium2 on PYTHONPATH.

    pdftools.py raster DPI PAGES IN.pdf PREFIX   writes PREFIX-1.png, PREFIX-2.png...
    pdftools.py text PAGES IN.pdf                prints the text in reading order
    pdftools.py layout PAGES IN.pdf              prints the text line by line, by position

The text of each page ends with a form feed, as with pdftotext. Only the
first PAGES pages are read.
"""
import ctypes
import os
import resource
import struct
import sys
import zlib

MEMORY_MB = int(os.environ.get("DOCIBIS_PDF_MEMORY_MB", "1024"))


def png(path, width, height, rows):
    """Writes a grey PNG."""
    def chunk(kind, data):
        return struct.pack(">I", len(data)) + kind + data + struct.pack(">I", zlib.crc32(kind + data))
    raw = b"".join(b"\x00" + row for row in rows)
    with open(path, "wb") as f:
        f.write(b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 0, 0, 0, 0))
                + chunk(b"IDAT", zlib.compress(raw, 1)) + chunk(b"IEND", b""))


def raster(pdf, pages, dpi, prefix):
    for i in range(pages):
        bitmap = pdf[i].render(scale=dpi / 72, grayscale=True)
        width, height, stride = bitmap.width, bitmap.height, bitmap.stride
        data = bytes(bitmap.buffer)
        png(f"{prefix}-{i + 1}.png", width, height, (data[y * stride:y * stride + width] for y in range(height)))


def write(s):
    sys.stdout.buffer.write(s.encode("utf-8", "replace"))


def text(pdf, pages):
    for i in range(pages):
        write(pdf[i].get_textpage().get_text_range().replace("\r\n", "\n").replace("\r", "\n") + "\f")


def layout(pdf, pages, pdfium_c):
    """The characters line by line, top to bottom and left to right, spaces
    aside: the same text laid out the same way reads the same, whatever the
    order the PDF writes it in."""
    x, y = ctypes.c_double(), ctypes.c_double()
    for i in range(pages):
        page = pdf[i].get_textpage()
        chars = []
        for c in range(page.count_chars()):
            u = pdfium_c.FPDFText_GetUnicode(page.raw, c)
            if u < 33 or u > 0x10FFFF or 0xD800 <= u <= 0xDFFF or chr(u).isspace():
                continue
            pdfium_c.FPDFText_GetCharOrigin(page.raw, c, x, y)
            chars.append((-y.value, x.value, pdfium_c.FPDFText_GetFontSize(page.raw, c), chr(u)))
        chars.sort(key=lambda ch: (ch[0], ch[1]))
        lines = []
        for ch in chars:
            if lines and ch[0] - lines[-1][0] <= max(1.0, 0.3 * ch[2]):
                lines[-1][1].append(ch)
            else:
                lines.append((ch[0], [ch]))
        write("\n".join("".join(ch[3] for ch in sorted(line, key=lambda ch: ch[1])) for _, line in lines) + "\f")


def main(args):
    resource.setrlimit(resource.RLIMIT_AS, (MEMORY_MB << 20, MEMORY_MB << 20))
    import pypdfium2 as pdfium
    import pypdfium2.raw as pdfium_c
    command = args[0] if args else ""
    if command == "raster" and len(args) == 5:
        dpi, most, src = int(args[1]), int(args[2]), args[3]
    elif command in ("text", "layout") and len(args) == 3:
        most, src = int(args[1]), args[2]
    else:
        sys.stderr.write(__doc__)
        return 2
    pdf = pdfium.PdfDocument(src)
    pages = min(len(pdf), most)
    if command == "raster":
        raster(pdf, pages, dpi, args[4])
    elif command == "text":
        text(pdf, pages)
    else:
        layout(pdf, pages, pdfium_c)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
