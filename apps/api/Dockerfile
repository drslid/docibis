# DocIbis API image: the Go server and LibreOffice (official build, checked by
# SHA-256), linux/amd64.

ARG GO_IMAGE=public.ecr.aws/docker/library/golang:1.27-trixie
ARG BASE_IMAGE=public.ecr.aws/docker/library/debian:trixie-slim

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The release workflow writes VERSION and CHANGES (the notes of the
# "What's new" page); local builds may pass --build-arg VERSION=...
ARG VERSION=dev
RUN if [ -f VERSION ]; then VERSION=$(cat VERSION); fi \
 && CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X docibis/internal/version.Version=${VERSION}" \
      -o /out/docibis ./cmd/docibis \
 && if [ -f CHANGES ]; then cp CHANGES /out/changes.txt; else : > /out/changes.txt; fi \
 && chmod 644 /out/changes.txt

# LibreOffice is unpacked from the official .deb files, without the desktop
# parts: only the files of the chosen packages reach the final image. Its
# bundled Python (pyuno) runs the worker, which drives LibreOffice by UNO.
FROM ${BASE_IMAGE} AS libreoffice
ARG LO_VERSION=26.8.1
# The release moves to the archive, under its build number, once the next one is out.
ARG LO_BUILD=26.8.1.1
ARG LO_SHA256=30903df3b9f61360d9660cd707de48cd2831469114492a5008ed58a0ac77d044
ARG LO_PACKAGES="libreoffice26.8-ure libobasis26.8-core libreoffice26.8 \
  libobasis26.8-writer libreoffice26.8-writer libobasis26.8-graphicfilter \
  libobasis26.8-pyuno libobasis26.8-ooolinguistic libreoffice26.8-dict-fr \
  libobasis26.8-en-us libreoffice26.8-en-us"
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl \
 && { curl -fsSL --retry 5 --retry-all-errors -o /tmp/lo.tar.gz \
        "https://download.documentfoundation.org/libreoffice/stable/${LO_VERSION}/deb/x86_64/LibreOffice_${LO_VERSION}_Linux_x86-64_deb.tar.gz" \
      || curl -fsSL --retry 5 --retry-all-errors -o /tmp/lo.tar.gz \
        "https://downloadarchive.documentfoundation.org/libreoffice/old/${LO_BUILD}/deb/x86_64/LibreOffice_${LO_BUILD}_Linux_x86-64_deb.tar.gz"; } \
 && echo "${LO_SHA256}  /tmp/lo.tar.gz" | sha256sum -c - \
 && mkdir /tmp/lo /staging \
 && tar -xzf /tmp/lo.tar.gz -C /tmp/lo --strip-components=2 --wildcards '*/DEBS/*.deb' \
 && for p in ${LO_PACKAGES}; do dpkg-deb -x /tmp/lo/${p}_*.deb /staging; done \
 && test -x /staging/opt/libreoffice26.8/program/soffice \
 && test -x /staging/opt/libreoffice26.8/program/python

FROM ${BASE_IMAGE}
LABEL org.opencontainers.image.title="DocIbis API" \
      org.opencontainers.image.description="Documents from Word templates and JSON data, rendered by LibreOffice" \
      org.opencontainers.image.source="https://github.com/drslid/docibis" \
      org.opencontainers.image.licenses="MIT"
# Libraries LibreOffice needs without a screen, and free fonts metrically
# compatible with the Office ones (Calibri, Cambria, Arial, Times New Roman…).
# Licensed fonts are mounted in /usr/local/share/fonts/docibis (see compose.yaml).
# poppler-utils (pdftoppm) draws the pages compared with the reference PDFs.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates fontconfig poppler-utils \
      libcairo2 libcups2t64 libdbus-1-3 libglib2.0-0t64 libnss3 libx11-xcb1 libxext6 libxinerama1 \
      fonts-liberation2 fonts-crosextra-carlito fonts-crosextra-caladea fonts-dejavu-core fonts-noto-core \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 docibis \
 && useradd --system --uid 10001 --gid 10001 --home-dir /data --no-create-home --shell /usr/sbin/nologin docibis \
 && mkdir -p /data /usr/local/share/fonts/docibis \
 && chown docibis:docibis /data
COPY --from=libreoffice /staging/opt/libreoffice26.8 /opt/libreoffice26.8
RUN fc-cache -f
COPY --from=build /out/docibis /usr/local/bin/
COPY --from=build /out/changes.txt /usr/local/lib/docibis/
COPY worker/worker.py /usr/local/lib/docibis/
# The worker must at least parse with the Python of LibreOffice.
RUN /opt/libreoffice26.8/program/python -c "import ast, sys; ast.parse(open(sys.argv[1]).read())" /usr/local/lib/docibis/worker.py
ENV DATA_DIR=/data \
    HOME=/tmp \
    LO_PATH=/opt/libreoffice26.8/program
USER 10001:10001
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=4s --start-period=60s --retries=3 CMD ["docibis", "healthcheck"]
ENTRYPOINT ["docibis"]
CMD ["serve"]
