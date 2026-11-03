#!/bin/bash
# Builds /rootfs, the system part of the DocIbis image without a shell: the
# Debian packages whose libraries LibreOffice, its Python, Typst and PDFium
# load (found with ldd), the fonts and the certificates, copied without their
# scripts, documentation and translations. Each package keeps its dpkg record
# in /var/lib/dpkg/status.d, as in the distroless images, so that scanners
# still list it with its CVEs.
set -euo pipefail

root=/rootfs
always="base-files libc6 ca-certificates fontconfig-config fonts-liberation2 fonts-crosextra-carlito fonts-crosextra-caladea fonts-dejavu-core fonts-noto-core"

# Python modules the workers never load: their libraries (readline, curses,
# Tk) would only bring CVEs. ldd fails on files that are not ELF: ignored.
owners() {
  { xargs -0 ldd 2>/dev/null || true; } | awk '$2 == "=>" && $3 ~ /^\/(usr\/)?lib/ {print $3}' | sort -u \
    | while read -r lib; do dpkg-query -S "$(readlink -f "$lib")" 2>/dev/null || dpkg-query -S "$lib"; done \
    | cut -d: -f1 | sort -u
}
packages=$(find /opt/libreoffice26.8 /usr/local/lib/docibis/python -type f \( -name '*.so*' -o -perm -u+x \) \
  ! -name 'readline*' ! -name '_curses*' ! -name '_tkinter*' -print0 | owners)
packages=$(printf '%s\n' $packages $always | sort -u)
# Then what the libraries of these packages load themselves (NSS loads its
# softokn, which needs SQLite, for instance), until nothing is added.
while :; do
  more=$(for package in $packages; do dpkg-query -L "$package"; done | grep -E '\.so(\.[0-9]+)*$' \
    | while read -r file; do [ -f "$file" ] && printf '%s\0' "$file"; done | owners)
  next=$(printf '%s\n' $packages $more | sort -u)
  [ "$next" = "$packages" ] && break
  packages=$next
done

mkdir -p "$root/var/lib/dpkg/status.d"
for package in $packages; do
  dpkg-query -L "$package" | while IFS= read -r file; do
    if [ -f "$file" ] || [ -L "$file" ]; then printf '%s\n' "$file"; fi
  done | awk '!/^\/usr\/share\/(man|info|lintian|bug|menu|locale)\// && (!/^\/usr\/share\/doc\// || /\/copyright$/)' \
    | tar -cf - --no-recursion -T - | tar -xf - -C "$root"
  dpkg-query -s "$package" > "$root/var/lib/dpkg/status.d/$package"
done

# What the packages' scripts would have made. Licensed fonts are mounted in
# /usr/local/share/fonts/docibis, which fontconfig reads at start.
cp -a /etc/fonts "$root/etc/"
mkdir -p "$root/usr/local/share/fonts/docibis"
fc-cache -f --sysroot "$root"
mkdir -p "$root/etc/ssl/certs"
cp /etc/ssl/certs/ca-certificates.crt "$root/etc/ssl/certs/"
echo 'include /etc/ld.so.conf.d/*.conf' > "$root/etc/ld.so.conf"
ldconfig -r "$root"
printf 'root:x:0:0:root:/root:/sbin/nologin\nnobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin\ndocibis:x:10001:10001:DocIbis:/data:/sbin/nologin\n' > "$root/etc/passwd"
printf 'root:x:0:\nnogroup:x:65534:\ndocibis:x:10001:\n' > "$root/etc/group"
printf 'passwd: files\ngroup: files\nhosts: files dns\n' > "$root/etc/nsswitch.conf"
mkdir -p "$root/tmp" "$root/data"
chmod 1777 "$root/tmp"
chown 10001:10001 "$root/data"

echo "$(echo "$packages" | wc -w) packages:" $packages
