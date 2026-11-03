# DocIbis console: static files served by Caddy, which also forwards the API
# paths to the api service and, with a domain name, provides HTTPS.

ARG NODE_IMAGE=public.ecr.aws/docker/library/node:24-alpine3.23
ARG GO_IMAGE=public.ecr.aws/docker/library/golang:1.27-trixie
ARG CADDY_IMAGE=public.ecr.aws/docker/library/caddy:2.11-alpine

# The static files are the same on every architecture.
FROM --platform=$BUILDPLATFORM ${NODE_IMAGE} AS build
WORKDIR /src
COPY package.json package-lock.json .npmrc ./
RUN npm ci --audit=false --fund=false --loglevel warn
COPY . .
RUN npm run build

# Caddy rebuilt with the current Go and the fixed versions of the libraries
# the official binary still has CVEs in (Trivy); same version, same modules.
FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS caddy
ARG TARGETARCH
ARG CADDY_VERSION=v2.11.7
ARG CADDY_FIXES="golang.org/x/net@v0.62.0 github.com/go-jose/go-jose/v4@v4.1.6 github.com/go-chi/chi/v5@v5.4.0 golang.org/x/crypto@v0.58.1-0.20261009181718-2f1f834330b2"
WORKDIR /src
RUN printf 'package main\n\nimport (\n\tcaddycmd "github.com/caddyserver/caddy/v2/cmd"\n\n\t_ "github.com/caddyserver/caddy/v2/modules/standard"\n)\n\nfunc main() { caddycmd.Main() }\n' > main.go \
 && go mod init docibis/caddy \
 && go get "github.com/caddyserver/caddy/v2@${CADDY_VERSION}" ${CADDY_FIXES} \
 && go mod tidy \
 && CGO_ENABLED=0 GOARCH=${TARGETARCH} go build -trimpath -ldflags "-s -w" -o /out/caddy . \
 && go version -m /out/caddy | grep -q "caddyserver/caddy/v2"

FROM ${CADDY_IMAGE}
LABEL org.opencontainers.image.title="DocIbis console" \
      org.opencontainers.image.source="https://github.com/drslid/docibis" \
      org.opencontainers.image.licenses="MIT"
# curl is not used (the health check runs wget): it goes, with its libraries;
# the other packages get the latest Alpine fixes.
COPY --from=caddy /out/caddy /usr/bin/caddy
# Ports 80 and 443 without root, for the HTTPS mode; certificates in a volume.
RUN apk del --no-cache curl && apk upgrade --no-cache \
 && apk add --no-cache libcap \
 && setcap cap_net_bind_service=+ep /usr/bin/caddy \
 && mkdir -p /var/lib/caddy \
 && chown 10001:10001 /var/lib/caddy
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /src/dist /srv
ENV API_UPSTREAM=api:8080 \
    XDG_CONFIG_HOME=/tmp/caddy/config \
    XDG_DATA_HOME=/var/lib/caddy
USER 10001:10001
EXPOSE 8080 80 443
HEALTHCHECK --interval=10s --timeout=3s --retries=3 CMD ["wget", "-q", "-Y", "off", "-O", "/dev/null", "http://127.0.0.1:8080/"]
