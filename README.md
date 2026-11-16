# Tattle

Tattle shows, in plain language, what every VM and container on a Proxmox host
is talking to: which websites and services they contact, who connects to them,
and how much data moves.

It is one program that runs on the Proxmox host. Nothing is installed inside
the guests.

## Try it without Proxmox

```bash
make demo
```

Then open http://127.0.0.1:8477. Demo mode shows invented traffic from a
pretend home lab. Building needs Go and pnpm.

## Install on a Proxmox host

Build the Linux binary on your computer and copy it over:

```bash
make linux
scp bin/tattle-linux-amd64 root@your-proxmox:/usr/local/bin/tattle
scp deploy/tattle.service root@your-proxmox:/etc/systemd/system/
```

Then on the host:

```bash
systemctl enable --now tattle
```

Open `http://your-proxmox:8477`.

Anyone who can reach that address can see the dashboard. To require a
password, uncomment the `TATTLE_PASSWORD` line in the service file. The
browser asks for a username too; any username works. The password travels
unencrypted unless you put Tattle behind an HTTPS reverse proxy.

## Settings

The Settings link in the dashboard controls what counts as your network
(the host's own networks are detected; you can add more) and whether the
dashboard hides traffic inside your network or internet traffic when it
opens. Settings are saved on the host and apply to everyone.

## Options

| Flag | Default | Meaning |
|---|---|---|
| `--listen` | `:8477` | Address for the dashboard |
| `--iface` | every `vmbr*` bridge | Bridges to watch, comma separated |
| `--db` | `/var/lib/tattle/tattle.db` | Where history is kept |
| `--retention` | `7` | Days of history to keep |
| `--local-net` | none | Extra networks to count as inside your network, comma separated |
| `--pve-dir` | `/etc/pve` | Proxmox config directory |
| `--demo` | off | Show invented traffic |

## How it works

All guest traffic crosses a bridge on the host (`vmbr0`). Tattle listens on the
bridge and, for each connection, records who started it, where it went, and how
many bytes moved in each direction. Totals are stored per minute in SQLite.

- **Machine names** come from the VM and container config files in `/etc/pve`,
  matched to traffic by network card (MAC) address.
- **Destination names** come from three places, in order of preference: the
  server name in the HTTPS handshake, the `Host` header of plain HTTP, and DNS
  answers seen on the bridge.

## What it cannot see

- **The contents of encrypted connections.** For HTTPS, Tattle knows the site
  and the amount of data, not the pages or what was sent.
- **Names for some connections.** A destination shows as a bare address when
  the guest uses encrypted DNS and the connection is HTTP/3, or when it
  connects by address.
- **Docker containers inside a VM.** They appear as the VM.
- **Guests that bypass the bridge**, such as a VM with a passed-through
  network card.

## Known limitations

- **Routed or NAT setups count some traffic twice.** If guests sit on an
  internal bridge and the host forwards their traffic out through another
  watched bridge, the same bytes show once for the guest and once for the
  host. Use `--iface` to watch only the internal bridge.
- **Tested on a simulated bridge, not yet on a real Proxmox host.** Capture,
  naming and attribution were verified in a Linux container with a bridge and
  a veth guest wired the way Proxmox wires containers.

## Development

```bash
make test
```

For UI work, run `make demo` in one terminal and `pnpm dev` in `web/` in
another; the dev server proxies `/api` to the running binary.

```
cmd/tattle         the program: flags and wiring
internal/capture   raw packet capture (Linux)
internal/decode    Ethernet/IP/TCP/UDP, DNS, TLS and HTTP parsing
internal/guests    reads VM and container configs
internal/tracker   turns packets into connections and attributes them
internal/store     SQLite history and queries
internal/api       JSON API and static file server
internal/demo      invented traffic
web                the dashboard (React + Vite)
```

## Licence

Apache-2.0. See [LICENSE](LICENSE).
