#!/bin/sh
# Installs, upgrades, removes and purges the package on this machine, and
# checks each step. It needs root and systemd, and changes the machine, so
# it is meant for a throwaway container or a CI runner.
#
# Usage: packaging/test-deb.sh path/to/tattle_VERSION_ARCH.deb
set -eu

DEB=$(realpath "${1:?path to the .deb}")
[ "$(id -u)" = 0 ] || { echo "run as root" >&2; exit 1; }
[ -d /run/systemd/system ] || { echo "systemd is not running here" >&2; exit 1; }

step() { printf '\n== %s\n' "$*"; }
fail() { echo "FAILED: $*" >&2; journalctl -u tattle --no-pager 2>/dev/null | tail -25 >&2; exit 1; }
api()  { curl -sk --max-time 10 "$@"; }

# Tattle looks for a Proxmox bridge; give it one to watch.
ip link show vmbr0 >/dev/null 2>&1 || { ip link add vmbr0 type bridge && ip link set vmbr0 up; }

step "install"
dpkg -i "$DEB"
sleep 3
systemctl is-enabled --quiet tattle || fail "the service is not enabled"
systemctl is-active  --quiet tattle || fail "the service is not running"
tattle --version
[ -f /usr/share/tattle/ip2asn.tsv.gz ] || fail "the address-owner table is missing"
[ "$(stat -c %a /var/lib/tattle/tls/key.pem)" = 600 ] || fail "the private key is readable by others"
[ "$(stat -c %a /var/lib/tattle)" = 700 ] || fail "the folder holding the history is open to other users"
[ "$(stat -c %a /var/lib/tattle/tattle.db)" = 600 ] || fail "the history is readable by other users"
journalctl -u tattle --no-pager | grep -q "watching vmbr0" || fail "it is not watching vmbr0"
if journalctl -u tattle --no-pager | grep -q "address-owner table"; then fail "the address-owner table was not found"; fi
[ "$(api https://127.0.0.1:8477/api/auth)" = '{"enabled":true,"setup":true,"signedIn":false}' ] || fail "unexpected answer from the dashboard"
[ "$(api -o /dev/null -w '%{http_code}' https://127.0.0.1:8477/api/status)" = 401 ] || fail "data is reachable without signing in"

step "first-time setup"
CODE=$(journalctl -u tattle --no-pager | sed -n 's/.*setup code \(.*\)/\1/p' | tail -1)
[ -n "$CODE" ] || fail "no setup code in the log"
JAR=$(mktemp)
[ "$(api -o /dev/null -w '%{http_code}' -c "$JAR" -H 'Content-Type: application/json' \
	-d "{\"code\":\"$CODE\",\"password\":\"package-test-password\"}" https://127.0.0.1:8477/api/auth/setup)" = 204 ] || fail "setup was refused"
api -b "$JAR" https://127.0.0.1:8477/api/health | grep -q '"id":"capture","title":"Watching the network","state":"ok"' || fail "capture is not healthy"
api -b "$JAR" https://127.0.0.1:8477/api/status | grep -q '"ownerRanges":[1-9]' || fail "the address-owner table is not loaded"

step "how tightly the service is confined (lower is better)"
systemd-analyze security tattle.service 2>/dev/null | tail -1 || true

step "upgrade"
dpkg -i "$DEB"
sleep 3
systemctl is-active --quiet tattle || fail "the service is not running after an upgrade"
[ "$(api -b "$JAR" -o /dev/null -w '%{http_code}' https://127.0.0.1:8477/api/status)" = 200 ] || fail "the upgrade lost the password or the session"

step "an upgrade leaves a stopped service stopped"
systemctl stop tattle
dpkg -i "$DEB"
sleep 2
if systemctl is-active --quiet tattle; then fail "an upgrade started a service its owner had stopped"; fi
systemctl start tattle
sleep 2

step "remove"
dpkg -r tattle
if systemctl is-active --quiet tattle; then fail "the service is still running after removal"; fi
[ ! -e /usr/bin/tattle ] || fail "the program is still installed"
[ -f /var/lib/tattle/tattle.db ] || fail "removal deleted the history; only purge should"

step "reinstall keeps the password"
dpkg -i "$DEB"
sleep 3
systemctl is-active --quiet tattle || fail "the service did not start again after a reinstall"
[ "$(api https://127.0.0.1:8477/api/auth)" = '{"enabled":true,"setup":false,"signedIn":false}' ] || fail "the password did not survive remove and reinstall"

step "purge"
dpkg -P tattle
[ ! -e /var/lib/tattle ] || fail "purge left the history behind"
[ ! -e /lib/systemd/system/tattle.service ] || fail "purge left the service file behind"
if systemctl is-active --quiet tattle; then fail "the service is still running after purge"; fi

printf '\nAll package checks passed.\n'
