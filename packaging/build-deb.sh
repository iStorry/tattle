#!/bin/sh
# Builds dist/tattle_VERSION_ARCH.deb from files that are already built:
#
#   bin/tattle-linux-ARCH     the program            (make linux)
#   data/ip2asn.tsv.gz        who owns which address (make data)
#
# Usage: packaging/build-deb.sh VERSION ARCH      e.g. 0.1.0 amd64
set -eu

VERSION=${1:?version, such as 0.1.0}
ARCH=${2:?architecture: amd64 or arm64}
HERE=$(cd "$(dirname "$0")" && pwd)
TOP=$(dirname "$HERE")

for needed in "$TOP/bin/tattle-linux-$ARCH" "$TOP/data/ip2asn.tsv.gz"; do
	[ -f "$needed" ] || { echo "missing $needed; run 'make linux data' first" >&2; exit 1; }
done

# The package is assembled in a scratch folder that is removed afterwards.
ROOT=$(mktemp -d)
trap 'rm -r "${ROOT:?}"' EXIT
chmod 0755 "$ROOT" # it stands in for "/" inside the package

install -D -m 0755 "$TOP/bin/tattle-linux-$ARCH" "$ROOT/usr/bin/tattle"
install -D -m 0644 "$TOP/deploy/tattle.service"  "$ROOT/lib/systemd/system/tattle.service"
# -p keeps the table's date, which is when it was published.
install -D -p -m 0644 "$TOP/data/ip2asn.tsv.gz"  "$ROOT/usr/share/tattle/ip2asn.tsv.gz"
install -D -m 0644 "$TOP/LICENSE"                "$ROOT/usr/share/doc/tattle/copyright"

mkdir -p "$ROOT/DEBIAN"
cat > "$ROOT/DEBIAN/control" <<CONTROL
Package: tattle
Version: $VERSION
Architecture: $ARCH
Maintainer: iStorry <istorry@users.noreply.github.com>
Installed-Size: $(du -sk "$ROOT" | cut -f1)
Section: net
Priority: optional
Homepage: https://github.com/istorry/tattle
Description: See what your Proxmox VMs and containers are talking to
 Tattle watches the network bridges on a Proxmox host and shows, in plain
 language, which websites and services every VM and container contacts, who
 connects to them, and how much data moves. Nothing is installed inside the
 guests, and nothing leaves the host.
CONTROL
for script in postinst prerm postrm; do
	install -m 0755 "$HERE/$script" "$ROOT/DEBIAN/$script"
done

mkdir -p "$TOP/dist"
dpkg-deb --root-owner-group --build "$ROOT" "$TOP/dist/tattle_${VERSION}_${ARCH}.deb"
