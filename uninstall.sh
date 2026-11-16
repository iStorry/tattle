#!/usr/bin/env bash
# Removes Tattle from this host.
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/istorry/tattle/main/uninstall.sh)"
#
# It stops the service and removes the package. It then asks whether to
# delete the recorded history, the dashboard password and the certificate as
# well. To answer in advance, for example from a script:
#
#   TATTLE_PURGE=yes   delete them too
#   TATTLE_PURGE=no    keep them, so reinstalling carries on where it left off

set -euo pipefail

DATA=/var/lib/tattle
OVERRIDES=/etc/systemd/system/tattle.service.d

say()  { printf '%s\n' "$*"; }
fail() { printf '\nTattle was not removed: %s\n' "$*" >&2; exit 1; }

# state prints dpkg's status for the package: "installed", "config-files"
# (removed, with its data kept), or "none".
state() {
	local status
	status=$(dpkg-query --show --showformat='${db:Status-Status}' tattle 2>/dev/null || true)
	say "${status:-none}"
}

main() {
	# The whole script is inside this function and it is called on the last
	# line, so a download that is cut short runs nothing.

	[ "$(id -u)" = 0 ] || fail "run this as root."
	if ! command -v dpkg >/dev/null || ! command -v apt-get >/dev/null; then
		fail "apt was not found, so Tattle cannot have been installed from its package here."
	fi

	local was
	was=$(state)
	if [ "$was" = none ] || [ "$was" = not-installed ]; then
		if [ ! -e "$DATA" ]; then
			say "Tattle is not installed, and it has left nothing behind."
			return 0
		fi
		was=config-files # its data is still here
	fi

	# Decide about the data before changing anything.
	local purge="${TATTLE_PURGE:-}"
	case "$purge" in
		yes | no) ;;
		"")
			if [ -t 0 ]; then
				say "Tattle keeps its recorded history, the dashboard password and its"
				say "certificate in $DATA."
				say
				local answer=""
				read -r -p "Delete those as well? This cannot be undone. [y/N] " answer || true
				case "$answer" in y | Y | yes | Yes | YES) purge=yes ;; *) purge=no ;; esac
				say
			else
				purge=no # nobody to ask, so do the thing that can be undone
			fi
			;;
		*) fail "TATTLE_PURGE must be yes or no, not \"$purge\"." ;;
	esac

	if [ "$was" = installed ]; then
		say "Stopping and removing Tattle."
	fi
	if [ "$purge" = yes ]; then
		DEBIAN_FRONTEND=noninteractive apt-get purge --yes --quiet tattle ||
			fail "apt could not remove the package. Its messages are above."
		# Changes made with "systemctl edit tattle" belong to nobody's
		# package, so apt leaves them.
		if [ -d "$OVERRIDES" ]; then
			rm -r "$OVERRIDES"
			systemctl daemon-reload || true
		fi
		# In case the folder was created by running tattle by hand.
		if [ -d "$DATA" ]; then
			rm -r "$DATA"
		fi
	elif [ "$was" = installed ]; then
		DEBIAN_FRONTEND=noninteractive apt-get remove --yes --quiet tattle ||
			fail "apt could not remove the package. Its messages are above."
	fi

	if systemctl is-active --quiet tattle 2>/dev/null; then
		fail "the package is gone but the service is still running. Stop it with: systemctl stop tattle"
	fi

	say
	if [ "$purge" = yes ]; then
		say "Tattle is removed, along with its history, password and certificate."
	elif [ "$was" = installed ]; then
		say "Tattle is removed. Its history, password and certificate are kept in"
		say "$DATA, so installing it again carries on where it left off."
		say "To delete those too, run this script again and answer yes."
	else
		say "Tattle was already removed. Its history, password and certificate are"
		say "still in $DATA."
		say "To delete those too, run this script again and answer yes."
	fi
}

main "$@"
