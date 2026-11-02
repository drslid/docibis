#!/usr/bin/env bash
# DocIbis installer: Docker, configuration, images, start and first sign-in.
#
#   curl -fsSL https://raw.githubusercontent.com/drslid/docibis/main/install.sh | sudo bash
#
# Run it again to update: the configuration (.env) and the data are kept.
# Without a terminal (or with DOCIBIS_YES=1) the answers come from these
# variables: DOCIBIS_DB (sqlite, postgres or external), DOCIBIS_DATABASE_URL,
# DOCIBIS_DOMAIN, DOCIBIS_PORT.
set -euo pipefail

REF=${DOCIBIS_REF:-main}
REPO=${DOCIBIS_REPO:-drslid/docibis}
DIR=${DOCIBIS_DIR:-/opt/docibis}
SOURCE=${DOCIBIS_SOURCE:-} # a local checkout instead of the GitHub archive

bold=$(tput bold 2>/dev/null || true)
plain=$(tput sgr0 2>/dev/null || true)
say() { printf '%s==>%s %s\n' "$bold" "$plain" "$*"; }
warn() { printf '%s!!%s  %s\n' "$bold" "$plain" "$*" >&2; }
die() { printf '%sxx%s  %s\n' "$bold" "$plain" "$*" >&2; exit 1; }

interactive=false
if [[ -z ${DOCIBIS_YES:-} ]] && { : </dev/tty; } 2>/dev/null; then
  interactive=true
fi

# ask VAR "Question" default: the answer, or the default without a terminal.
ask() {
  local answer=""
  if $interactive; then
    read -r -p "$2 [$3] " answer </dev/tty || true
  fi
  printf -v "$1" '%s' "${answer:-$3}"
}

secret() { head -c 64 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c "$1"; }

# 1. The machine.
[[ $(uname -s) == Linux ]] || die "DocIbis installs on Linux."
[[ $EUID -eq 0 ]] || die "Run as root: curl -fsSL … | sudo bash"
case $(uname -m) in
  x86_64 | amd64) ;;
  *) die "The DocIbis image is built for linux/amd64 (LibreOffice); this machine is $(uname -m)." ;;
esac
memory=$(awk '/MemTotal/ {print int($2 / 1024)}' /proc/meminfo)
((memory >= 1800)) || warn "${memory} MB of memory: 2 GB at least are advised (LibreOffice)."
mkdir -p "$DIR"
free=$(df -Pm "$DIR" | awk 'NR == 2 {print $4}')
((free >= 4000)) || warn "${free} MB free in $DIR: 4 GB at least are advised."

# 2. Docker and Compose.
if ! command -v docker >/dev/null; then
  install=""
  ask install "Docker is missing. Install Docker Engine from get.docker.com?" yes
  [[ $install == y* ]] || die "Docker is needed."
  curl -fsSL https://get.docker.com | sh
fi
docker compose version >/dev/null 2>&1 || die "Docker Compose v2 is needed (docker compose plugin)."
compose=$(docker compose version --short | sed 's/^v//')
[[ $(printf '%s\n' 2.20.0 "$compose" | sort -V | head -1) == 2.20.0 ]] || die "Docker Compose $compose: 2.20 or later is needed."

# 3. The files: compose.yaml and the sources (to build when no image exists).
say "DocIbis $REF in $DIR"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
if [[ -n $SOURCE ]]; then
  cp -R "$SOURCE"/. "$work/"
  rm -rf "$work/.git"
else
  curl -fsSL "https://codeload.github.com/$REPO/tar.gz/$REF" | tar -xz -C "$work" --strip-components=1
fi
[[ -f $work/compose.yaml ]] || die "No compose.yaml in $REPO@$REF."
rm -rf "$DIR/apps"
cp -R "$work/apps" "$work/compose.yaml" "$DIR/"
mkdir -p "$DIR/fonts"
cd "$DIR"

# 4. The configuration, written once and kept by later runs.
version=edge
[[ $REF =~ ^v[0-9] ]] && version=$REF
if [[ -f .env ]]; then
  say "Configuration kept: $DIR/.env"
  sed -i "s/^DOCIBIS_VERSION=.*/DOCIBIS_VERSION=$version/" .env
else
  echo
  echo "Database:"
  echo "  1) SQLite in the Docker volume: nothing to set up (one server)"
  echo "  2) PostgreSQL 18 in Docker: one more container, ready for several API instances"
  echo "  3) An existing PostgreSQL (15 or later)"
  case ${DOCIBIS_DB:-} in postgres) default=2 ;; external) default=3 ;; *) default=1 ;; esac
  db=""
  ask db "Choice" "$default"
  database_url="" profiles="" postgres_password=""
  case $db in
    2)
      profiles=postgres
      postgres_password=$(secret 32)
      database_url="postgres://docibis:$postgres_password@postgres:5432/docibis?sslmode=disable"
      ;;
    3)
      ask database_url "PostgreSQL address (postgres://user:password@host:5432/docibis)" "${DOCIBIS_DATABASE_URL:-}"
      [[ $database_url == postgres://* || $database_url == postgresql://* ]] || die "A postgres:// address is needed."
      ;;
  esac

  echo
  ask domain "Domain name for HTTPS with an automatic certificate (empty: plain HTTP)" "${DOCIBIS_DOMAIN:-}"
  port=8080 bind=0.0.0.0 files=compose.yaml
  if [[ -n $domain ]]; then
    bind=127.0.0.1 files=compose.yaml:apps/web/https.compose.yaml
  else
    ask port "HTTP port" "${DOCIBIS_PORT:-8080}"
  fi

  umask 077
  cat >.env <<EOF
# DocIbis configuration, written by install.sh. Keep APP_SECRET_KEY: without
# it, the second-factor secrets of the accounts cannot be read any more.
APP_SECRET_KEY=$(secret 48)
INSTANCE_NAME=PROD
DOCIBIS_VERSION=$version
DOCIBIS_BIND=$bind
DOCIBIS_PORT=$port
DOCIBIS_DOMAIN=$domain
COMPOSE_FILE=$files
COMPOSE_PROFILES=$profiles
POSTGRES_PASSWORD=$postgres_password
DATABASE_URL='$database_url'

# Optional continuous copy of the SQLite database to S3, R2 or MinIO:
# STATE_URL=s3://bucket/docibis?region=eu-west-3
# AWS_ACCESS_KEY_ID=
# AWS_SECRET_ACCESS_KEY=
EOF
  umask 022
  say "Configuration written: $DIR/.env"
fi

# 5. Images: from GHCR, or built here when the version has none.
if docker compose pull --quiet api web 2>/dev/null; then
  say "Images pulled ($version)"
else
  say "No published image for $version: building here (a few minutes)"
  docker compose build
fi

# 6. Start, then wait for the API.
docker compose up -d --remove-orphans
port=$(sed -n 's/^DOCIBIS_PORT=//p' .env)
for _ in $(seq 120); do
  curl --noproxy '*' -fsS "http://127.0.0.1:${port:-8080}/api/auth/session" -o "$work/session.json" 2>/dev/null && break
  sleep 2
done
[[ -s $work/session.json ]] || die "DocIbis does not answer: cd $DIR && docker compose logs api"

domain=$(sed -n 's/^DOCIBIS_DOMAIN=//p' .env)
if [[ -n $domain ]]; then
  url="https://$domain"
else
  host=$(hostname -I 2>/dev/null | awk '{print $1}')
  url="http://${host:-localhost}:${port:-8080}"
fi
echo
say "DocIbis is running: $url"
if grep -q '"setupRequired":true' "$work/session.json"; then
  code=$(docker compose logs api 2>/dev/null | grep -o '"setup_code":"[^"]*"' | tail -1 | cut -d'"' -f4)
  echo "    Setup code: ${bold}${code}${plain}"
  echo "    Open the address, enter this code and create the first administrator."
fi
echo "    Folder: $DIR (configuration in .env, fonts in fonts/)"
echo "    Logs:   cd $DIR && docker compose logs -f"
