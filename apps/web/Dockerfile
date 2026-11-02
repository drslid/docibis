# DocIbis console: static files served by Caddy, which also forwards the API
# paths to the api service and, with a domain name, provides HTTPS.

ARG NODE_IMAGE=public.ecr.aws/docker/library/node:24-alpine3.23
ARG CADDY_IMAGE=public.ecr.aws/docker/library/caddy:2.11-alpine

FROM ${NODE_IMAGE} AS build
WORKDIR /src
COPY package.json package-lock.json .npmrc ./
RUN npm ci --audit=false --fund=false --loglevel warn
COPY . .
RUN npm run build

FROM ${CADDY_IMAGE}
LABEL org.opencontainers.image.title="DocIbis console" \
      org.opencontainers.image.source="https://github.com/drslid/docibis" \
      org.opencontainers.image.licenses="MIT"
# Ports 80 and 443 without root, for the HTTPS mode; certificates in a volume.
RUN apk add --no-cache libcap \
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
