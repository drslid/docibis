# DocIbis API image: the Go server and LibreOffice (official build, checked by
# SHA-256), for linux/amd64 and linux/arm64 (Graviton). The final image has no
# shell and no package manager (distroless): only the files the service runs,
# from Debian packages still listed for image scanners.

ARG GO_IMAGE=public.ecr.aws/docker/library/golang:1.27-trixie
ARG BASE_IMAGE=public.ecr.aws/docker/library/debian:trixie-slim

# Go cross-compiles: this stage runs on the machine that builds.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release workflow writes VERSION and CHANGES (the notes of the
# "What's new" page); local builds may pass --build-arg VERSION=...
ARG VERSION=dev
RUN if [ -f VERSION ]; then VERSION=$(cat VERSION); fi \
 && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -tags timetzdata \
      -ldflags "-s -w -X docibis/internal/version.Version=${VERSION}" \
      -o /out/docibis ./cmd/docibis \
 && if [ -f CHANGES ]; then cp CHANGES /out/changes.txt; else : > /out/changes.txt; fi \
 && chmod 644 /out/changes.txt

# LibreOffice is unpacked from the official .deb files, without the desktop
# parts: only the files of the chosen packages reach the final image, with
# their package records. Its bundled Python (pyuno) runs the workers, which
# drive LibreOffice by UNO. Unpacking needs no emulation: this stage runs on
# the machine that builds, for the target architecture.
FROM --platform=$BUILDPLATFORM ${BASE_IMAGE} AS libreoffice
ARG TARGETARCH
ARG LO_VERSION=26.8.1
# The release moves to the archive, under its build number, once the next one is out.
ARG LO_BUILD=26.8.1.1
ARG LO_SHA256_AMD64=30903df3b9f61360d9660cd707de48cd2831469114492a5008ed58a0ac77d044
ARG LO_SHA256_ARM64=1b069c20dd237f6decad3ea02cb02f39fdf458b03d09c2a9facb7b9b71e6a27a
ARG LO_PACKAGES="libreoffice26.8-ure libobasis26.8-core libreoffice26.8 \
  libobasis26.8-writer libreoffice26.8-writer libobasis26.8-graphicfilter \
  libobasis26.8-pyuno libobasis26.8-ooolinguistic libreoffice26.8-dict-fr \
  libobasis26.8-en-us libreoffice26.8-en-us"
RUN case "${TARGETARCH}" in \
      amd64) arch=x86_64 name=x86-64 sum=${LO_SHA256_AMD64} ;; \
      arm64) arch=aarch64 name=aarch64 sum=${LO_SHA256_ARM64} ;; \
      *) echo "unsupported architecture: ${TARGETARCH}"; exit 1 ;; \
    esac \
 && apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && { curl -fsSL --retry 5 --retry-all-errors -o /tmp/lo.tar.gz \
        "https://download.documentfoundation.org/libreoffice/stable/${LO_VERSION}/deb/${arch}/LibreOffice_${LO_VERSION}_Linux_${name}_deb.tar.gz" \
      || curl -fsSL --retry 5 --retry-all-errors -o /tmp/lo.tar.gz \
        "https://downloadarchive.documentfoundation.org/libreoffice/old/${LO_BUILD}/deb/${arch}/LibreOffice_${LO_BUILD}_Linux_${name}_deb.tar.gz"; } \
 && echo "${sum}  /tmp/lo.tar.gz" | sha256sum -c - \
 && mkdir -p /tmp/lo /staging/status.d \
 && tar -xzf /tmp/lo.tar.gz -C /tmp/lo --strip-components=2 --wildcards '*/DEBS/*.deb' \
 && for p in ${LO_PACKAGES}; do \
      dpkg-deb -x /tmp/lo/${p}_*.deb /staging \
      && { echo "Status: install ok installed"; dpkg-deb -f /tmp/lo/${p}_*.deb; } > /staging/status.d/$p || exit 1; \
    done \
 && ln -s "$(basename /staging/opt/libreoffice26.8/program/python-core-*)" /staging/opt/libreoffice26.8/program/python-core \
 && test -x /staging/opt/libreoffice26.8/program/oosplash \
 && test -x /staging/opt/libreoffice26.8/program/python.bin \
 && test -d /staging/opt/libreoffice26.8/program/python-core/lib

# Typst for the native engine and PDFium (pypdfium2) to draw and read the PDFs
# compared: the official wheels (no compiler needed), checked by SHA-256 and
# unpacked for the Python of LibreOffice, with their metadata so that image
# scanners list them.
ARG TYPST_URL_AMD64=https://files.pythonhosted.org/packages/2b/57/e2bb9b7823c049361c9e7d2d971996430b71260bfc3a7ed289ca4b37c1b0/typst-0.15.0-cp38-abi3-manylinux_2_17_x86_64.manylinux2014_x86_64.whl
ARG TYPST_SHA256_AMD64=8f33d98451bab132a612b98ffc8d1830c97a076ea3f3fde11f6ff7ab9bcae89c
ARG TYPST_URL_ARM64=https://files.pythonhosted.org/packages/83/b5/29e6218486259056c2649fb245c5066c3a821cb8b56d6710c3007062136a/typst-0.15.0-cp38-abi3-manylinux_2_17_aarch64.manylinux2014_aarch64.whl
ARG TYPST_SHA256_ARM64=97350fcf5eebe5b6c75415e005ac42136744aa9950f4c0e4c484dc015e38d9de
ARG PDFIUM_URL_AMD64=https://files.pythonhosted.org/packages/4f/a3/c9cc797fc8bdfb8f37b9b0f8b9d02a5fc196b2015f408d53624cab5b0519/pypdfium2-5.14.0-py3-none-manylinux_2_17_x86_64.manylinux2014_x86_64.whl
ARG PDFIUM_SHA256_AMD64=11f281613fa22313d9c7ab89947665e84eccf8ebe40e1198a84a88352305648d
ARG PDFIUM_URL_ARM64=https://files.pythonhosted.org/packages/92/b4/0c31aa51887cd6cd032191dfe010a6d01ed43cf03204cfbd2184ebe4b715/pypdfium2-5.14.0-py3-none-manylinux_2_17_aarch64.manylinux2014_aarch64.whl
ARG PDFIUM_SHA256_ARM64=f1b696e6901e16f114a2ec6332e5e3f8f5033a901614ead28499ab18ca6024f5
RUN if [ "${TARGETARCH}" = arm64 ]; then \
      typst_url=${TYPST_URL_ARM64} typst_sum=${TYPST_SHA256_ARM64} pdfium_url=${PDFIUM_URL_ARM64} pdfium_sum=${PDFIUM_SHA256_ARM64}; \
    else \
      typst_url=${TYPST_URL_AMD64} typst_sum=${TYPST_SHA256_AMD64} pdfium_url=${PDFIUM_URL_AMD64} pdfium_sum=${PDFIUM_SHA256_AMD64}; \
    fi \
 && apt-get install -y --no-install-recommends unzip \
 && curl -fsSL --retry 5 --retry-all-errors -o /tmp/typst.whl "${typst_url}" \
 && echo "${typst_sum}  /tmp/typst.whl" | sha256sum -c - \
 && curl -fsSL --retry 5 --retry-all-errors -o /tmp/pdfium.whl "${pdfium_url}" \
 && echo "${pdfium_sum}  /tmp/pdfium.whl" | sha256sum -c - \
 && mkdir -p /staging/python \
 && unzip -q /tmp/typst.whl 'typst/*' 'typst-*.dist-info/*' -d /staging/python \
 && unzip -q /tmp/pdfium.whl 'pypdfium2/*' 'pypdfium2_raw/*' 'pypdfium2_cfg/*' 'pypdfium2-*.dist-info/*' -d /staging/python \
 && test -f /staging/python/typst/_typst.abi3.so \
 && test -f /staging/python/pypdfium2_raw/libpdfium.so

# The system part: the Debian packages whose libraries LibreOffice, its Python,
# Typst and PDFium load, free fonts metrically compatible with the Office ones
# (Calibri, Cambria, Arial, Times New Roman…) and the certificates
# (docker/rootfs.sh). Licensed fonts are mounted in
# /usr/local/share/fonts/docibis (see compose.yaml).
FROM ${BASE_IMAGE} AS runtime
RUN apt-get update \
 && apt-get upgrade -y \
 && apt-get install -y --no-install-recommends \
      ca-certificates fontconfig \
      libcairo2 libcups2t64 libdbus-1-3 libglib2.0-0t64 libnss3 libx11-xcb1 libxext6 libxinerama1 \
      fonts-liberation2 fonts-crosextra-carlito fonts-crosextra-caladea fonts-dejavu-core fonts-noto-core
COPY --from=libreoffice /staging/opt/libreoffice26.8 /opt/libreoffice26.8
COPY --from=libreoffice /staging/python /usr/local/lib/docibis/python
COPY docker/rootfs.sh /usr/local/bin/
RUN bash /usr/local/bin/rootfs.sh

FROM scratch
LABEL org.opencontainers.image.title="DocIbis API" \
      org.opencontainers.image.description="Documents from Word templates and JSON data, rendered by LibreOffice" \
      org.opencontainers.image.source="https://github.com/drslid/docibis" \
      org.opencontainers.image.licenses="MIT"
COPY --from=runtime /rootfs /
COPY --from=libreoffice /staging/opt/libreoffice26.8 /opt/libreoffice26.8
COPY --from=libreoffice /staging/status.d /var/lib/dpkg/status.d
COPY --from=libreoffice /staging/python /usr/local/lib/docibis/python
COPY --from=build /out/docibis /usr/local/bin/
COPY --from=build /out/changes.txt /usr/local/lib/docibis/
COPY worker/worker.py worker/native.py worker/pdftools.py /usr/local/lib/docibis/
# What the soffice and python shell scripts of LibreOffice set: the image runs
# oosplash and python.bin directly.
ENV PATH=/usr/local/bin:/opt/libreoffice26.8/program \
    DATA_DIR=/data \
    LO_PATH=/opt/libreoffice26.8/program \
    PYTHONHOME=/opt/libreoffice26.8/program/python-core \
    PYTHONPATH=/opt/libreoffice26.8/program:/opt/libreoffice26.8/program/python-core/lib:/opt/libreoffice26.8/program/python-core/lib/lib-dynload:/opt/libreoffice26.8/program/python-core/lib/site-packages:/usr/local/lib/docibis/python \
    UNO_PATH=/opt/libreoffice26.8/program \
    URE_BOOTSTRAP=vnd.sun.star.pathname:/opt/libreoffice26.8/program/fundamentalrc \
    DOCIBIS_WORKER="/opt/libreoffice26.8/program/python.bin /usr/local/lib/docibis/worker.py" \
    DOCIBIS_NATIVE_WORKER="/opt/libreoffice26.8/program/python.bin /usr/local/lib/docibis/native.py" \
    DOCIBIS_PDFTOOLS="/opt/libreoffice26.8/program/python.bin /usr/local/lib/docibis/pdftools.py"
# The workers must at least parse, and uno, Typst and PDFium load.
RUN ["/opt/libreoffice26.8/program/python.bin", "-c", "import ast, sys, uno, typst, pypdfium2; [ast.parse(open(f).read()) for f in sys.argv[1:]]", \
     "/usr/local/lib/docibis/worker.py", "/usr/local/lib/docibis/native.py", "/usr/local/lib/docibis/pdftools.py"]
# After the check, which runs as root: /tmp stays empty for the service.
ENV HOME=/tmp
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=4s --start-period=60s --retries=3 CMD ["docibis", "healthcheck"]
ENTRYPOINT ["docibis"]
CMD ["serve"]
