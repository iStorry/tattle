#!/usr/bin/env bash
# Installs or upgrades Tattle on a Proxmox host.
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/istorry/tattle/main/install.sh)"
#
# It downloads the package for this machine from Tattle's GitHub releases,
# checks it against the published checksums, and installs it with apt. It
# changes nothing else. uninstall.sh, beside this script, removes it again.
#
# Settings, all optional, given as environment variables:
#
#   TATTLE_VERSION   install this release, such as v0.1.0, not the latest
#   TATTLE_DEB       install this package file; nothing is downloaded
#   TATTLE_BASE_URL  download from here, for a mirror or for testing

set -euo pipefail

REPO="istorry/tattle"
BASE_URL="${TATTLE_BASE_URL:-https://github.com/$REPO}"

say()  { printf '%s\n' "$*"; }
fail() { printf '\nTattle was not installed: %s\n' "$*" >&2; exit 1; }

# fetch URL FILE downloads over HTTPS only, unless a test URL was given.
fetch() {
	local proto='=https'
	case "$BASE_URL" in http://*) proto='=http,https' ;; esac
	curl --fail --silent --show-error --location --proto "$proto" --tlsv1.2 --output "$2" "$1"
}

# latest_version prints the tag of the newest full release. GitHub answers
# /releases/latest with a redirect to it, which needs no API and no login.
# It returns 1 if GitHub could not be reached, and 2 if it was reached but
# there is no full release yet (GitHub then redirects to /releases instead;
# drafts and pre-releases never count as the latest).
latest_version() {
	local url
	url=$(curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 \
		--output /dev/null --write-out '%{url_effective}' "$BASE_URL/releases/latest") || return 1
	case "$url" in
		*/releases/tag/*) printf '%s\n' "${url##*/releases/tag/}" ;;
		*) return 2 ;;
	esac
}

main() {
	# The whole script is inside this function and it is called on the last
	# line, so a download that is cut short runs nothing.

	[ "$(id -u)" = 0 ] || fail "run this as root (on Proxmox, the root shell is what you have by default)."
	if ! command -v dpkg >/dev/null || ! command -v apt-get >/dev/null; then
		fail "this installer is for Proxmox and other Debian-based systems; apt was not found."
	fi
	command -v curl >/dev/null || fail "curl is needed to download Tattle; install it with: apt install curl"
	[ -d /run/systemd/system ] || fail "systemd is not running here, so Tattle could not be started as a service."

	local arch
	arch=$(dpkg --print-architecture)
	case "$arch" in
		amd64 | arm64) ;;
		*) fail "there is no package for this kind of processor ($arch); only amd64 and arm64 are built." ;;
	esac

	if [ ! -d /etc/pve ]; then
		say "Note: this does not look like a Proxmox host (there is no /etc/pve)."
		say "Tattle will install, but it will only find VMs and containers on Proxmox."
		say
	fi

	local before=""
	before=$(dpkg-query --show --showformat='${Version}' tattle 2>/dev/null || true)

	local tmp deb
	tmp=$(mktemp -d)
	# shellcheck disable=SC2064  # expand now: tmp is local to this function
	trap "rm -rf '$tmp'" EXIT

	if [ -n "${TATTLE_DEB:-}" ]; then
		[ -f "$TATTLE_DEB" ] || fail "there is no file at $TATTLE_DEB."
		deb="$tmp/$(basename "$TATTLE_DEB")"
		cp "$TATTLE_DEB" "$deb"
		say "Using the package file $(basename "$deb")."
	else
		local version="${TATTLE_VERSION:-}"
		if [ -z "$version" ]; then
			local status=0
			version=$(latest_version) || status=$?
			case "$status" in
				0) ;;
				2) fail "there is no full release of Tattle yet, only pre-releases. To install one, name it, such as TATTLE_VERSION=v0.1.0-rc1. See $BASE_URL/releases" ;;
				*) fail "could not reach $BASE_URL/releases to find the latest release. Check the internet connection, or name a release with TATTLE_VERSION=v0.1.0." ;;
			esac
		fi
		case "$version" in v*) ;; *) version="v$version" ;; esac

		local file="tattle_${version#v}_${arch}.deb"
		deb="$tmp/$file"
		say "Downloading Tattle ${version#v} for $arch."
		fetch "$BASE_URL/releases/download/$version/$file" "$deb" ||
			fail "could not download $file from release $version. Does that release exist? See $BASE_URL/releases"
		fetch "$BASE_URL/releases/download/$version/SHA256SUMS" "$tmp/SHA256SUMS" ||
			fail "could not download the checksums for release $version, so the package cannot be verified."

		# Refuse anything that is not exactly what was published.
		say "Checking the download."
		grep --quiet " $file\$" "$tmp/SHA256SUMS" ||
			fail "the published checksums do not mention $file, so the package cannot be verified."
		(cd "$tmp" && sha256sum --check --ignore-missing --status SHA256SUMS) ||
			fail "the download does not match its published checksum. Nothing was installed; try again, and if it happens twice, please report it."
	fi

	# The package must be the one for this machine, whatever its file is called.
	[ "$(dpkg-deb --field "$deb" Package 2>/dev/null)" = tattle ] || fail "$(basename "$deb") is not a Tattle package."
	[ "$(dpkg-deb --field "$deb" Architecture)" = "$arch" ] ||
		fail "$(basename "$deb") is for a different kind of processor than this machine ($arch)."

	say "Installing."
	# apt needs to be able to read the file as its own unprivileged user.
	chmod 0755 "$tmp" && chmod 0644 "$deb"
	DEBIAN_FRONTEND=noninteractive apt-get install --yes --quiet "$deb" ||
		fail "apt could not install the package. Its messages are above."

	local after
	after=$(dpkg-query --show --showformat='${Version}' tattle)
	say
	if [ -z "$before" ]; then
		say "Tattle $after is installed. What to do next is printed above."
	elif [ "$before" = "$after" ]; then
		say "Tattle $after was already installed and has been reinstalled."
	else
		say "Tattle was upgraded from $before to $after. Your history, settings and password are unchanged."
	fi
	if ! systemctl is-active --quiet tattle; then
		say
		say "The service is not running. To see why:  journalctl -u tattle -n 30 --no-pager"
		say "If you stopped it yourself, start it with: systemctl start tattle"
	fi
}

main "$@"
