# Changelog

What changed in each release, newest first. Versions follow
[semantic versioning](https://semver.org).

## 0.1.1 (2026-11-26)

- A network map: the router, each bridge, the machines on the host and
  every other device heard on the network, with when each was last heard
  and which machines it talked to. Devices are found by listening to the
  announcements they already make (ARP and IPv6 neighbour discovery);
  Tattle still sends nothing. Tattle now keeps the hardware address,
  address and first and last time heard of devices that never talk to a
  VM, a container or the host. See docs/privacy.md.
- `tattle update` upgrades to the latest release, or to one you name. It
  downloads the package from GitHub, checks it against the published
  checksums and installs it with apt.

## 0.1.0 (2026-11-16)

The first release.

### Seeing traffic

- Every VM, container and the host itself, by its Proxmox name, with no
  setup inside the guests.
- The places each machine contacted and the callers that connected to it,
  with the service in plain words, the amounts sent and received, and when.
- Names for destinations from HTTPS and HTTP/3 handshakes (including ones
  split across packets), plain HTTP, and DNS answers.
- A label on every connection saying whether it stayed inside your network
  or went to the internet, and a control to hide either side.
- A detail page for every destination and caller: its addresses, first and
  last seen, totals, traffic over time, and which machines use it.
- "What's new": the first time a machine contacts a place or a caller
  reaches a machine, after a one-day learning period for each machine.
- Names for devices on the network, from what they announce (mDNS and DHCP)
  or chosen by you.
- The organisation that owns an internet address, from a table kept on the
  host.
- Traffic that the host routes, masquerades or port-forwards for a guest is
  counted once, for the guest.

### Keeping history

- 90 days by default: the most recent 7 minute by minute, and the rest as
  hourly totals. Both periods can be changed in Settings.
- Views from 15 minutes to 90 days.
- A view can be bookmarked or shared: the selected machine, time range and
  filters are in the address.

### Running it

- A `.deb` package for amd64 and arm64 that installs Tattle as a service.
- Signing in with a password, set on first visit with a setup code from the
  host's log. Wrong guesses are limited. `tattle reset-password` clears a
  forgotten password.
- HTTPS by default, with a certificate Tattle generates and renews, or your
  own.
- A health page that checks capture, machine discovery, storage, the
  address-owner table and how the dashboard is protected, and says what to
  do when something is wrong.
- A demo mode with invented traffic, for trying Tattle without Proxmox.

### Known gaps

- Capture has not been measured at high traffic rates.
- The service runs as root.
