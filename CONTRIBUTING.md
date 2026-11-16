# Contributing to Tattle

Thank you for wanting to help. This page covers how to build and test
Tattle, how the code is laid out, and what makes a change easy to accept.

To report a security problem, see [SECURITY.md](SECURITY.md) instead.

## What Tattle is for

Tattle is for people who run a home lab and are not network engineers. Two
things follow from that, and most review comments come back to them:

- **Plain words.** The dashboard says "Secure web (HTTPS)" and "Inside your
  network", not "443/tcp" and "RFC 1918". Error messages say what happened
  and what to do. If a term needs explaining, use a different term.
- **Trust.** Tattle runs as root and sees every packet. Nothing leaves the
  host, text from the network is never trusted, and anything that parses
  packets is bounded and tested against cut-off and malformed input.

It is also deliberately a viewer. Blocking traffic, decrypting HTTPS,
downloading packets, user roles, cloud accounts and telemetry are out of
scope.

## Building

You need Go (the version in `go.mod`) and pnpm.

```bash
make demo
```

That builds everything and serves invented traffic at http://127.0.0.1:8477,
which is how most work on the dashboard is done. Live capture only works on
Linux; the demo works anywhere.

| Command | What it does |
|---|---|
| `make build` | Build the dashboard and the program for this computer |
| `make demo` | Build and run with invented traffic |
| `make test` | Vet and test the Go code |
| `make lint` | Check Go formatting and lint the dashboard |
| `make linux` | Build the Linux binaries, `amd64` and `arm64` |
| `make data` | Fetch the address-owner table into `data/` |
| `make deb` | Build the `.deb` packages into `dist/` (needs `dpkg-deb`) |

For dashboard work, run `make demo` in one terminal and `pnpm dev` in `web/`
in another. The dev server reloads as you edit and passes `/api` requests to
the running program. Add `--demo-login` to the demo to work on the sign-in
pages.

## Layout

```
cmd/tattle         the program: flags and wiring
internal/capture   raw packet capture (Linux)
internal/decode    Ethernet, IP, TCP, UDP, DNS, TLS, QUIC, HTTP, mDNS, DHCP parsing
internal/tracker   turns packets into connections and decides whose they are
internal/guests    reads VM and container configs from /etc/pve
internal/owners    the table of who owns which internet addresses
internal/store     SQLite: history, first sightings, device names, settings
internal/api       the JSON API, signing in, and the health checks
internal/serve     HTTPS and certificates
internal/demo      invented traffic
web                the dashboard (React, TypeScript, Vite)
packaging          the .deb package and its test
```

The dashboard is built into `internal/ui/dist` and embedded in the program,
which is why `make test` builds it first.

## Testing

Run `make lint test` before sending a change. CI runs the same, then builds
the package and installs, upgrades and removes it on the runner.

A few kinds of change need more than unit tests:

- **Anything that reads packets.** Add cases for input that is cut short at
  every length and for malformed input; the existing tests in
  `internal/decode` show the pattern. A parser must never panic and never
  read past what it was given.
- **Anything about attributing traffic.** The tracker tests build packets
  as they appear on each bridge. If you change how forwarded or translated
  traffic is counted, test it with real traffic too: a Linux container with
  a bridge and a network namespace as the guest behaves like a Proxmox
  container.
- **Packaging.** `packaging/test-deb.sh` takes a package through install,
  setup, upgrade, remove, reinstall and purge. It needs root and systemd and
  changes the machine, so run it in a throwaway container.

There is no test setup for the dashboard yet. Check changes there in the
demo, at desktop and phone width, in light and dark mode.

## Sending a change

- **Open an issue first for anything large,** so the approach can be agreed
  before you write it.
- **One change per pull request.** A fix and an unrelated tidy-up are two.
- **Say what you tested and what you could not.** "Not run on a real
  Proxmox host" is a useful thing to know.
- **Update the docs with the code:** the README, `docs/privacy.md` if what
  is collected changes, and `CHANGELOG.md` under "Unreleased".
- **Commit messages** start with a short line saying what the change does,
  then explain why. The log has examples.

## Licence

Tattle is licensed under Apache-2.0. By contributing, you agree that your
contribution is licensed under the same terms.
