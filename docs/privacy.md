# What Tattle collects

Tattle watches network traffic, so it is fair to ask exactly what it keeps
and where that goes. This page is the complete answer.

## In one paragraph

Tattle records who talked to whom, when, and how much. It does not record
what was said. Everything it records stays in one database on the Proxmox
host. Tattle itself never connects to anything: no telemetry, no update
checks, no lookups.

## What is recorded

For each connection to or from a VM, a container or the host:

| Recorded | Example |
|---|---|
| Which machine | `docker-host` (VM 101) |
| The other end's address | `198.51.100.60` |
| The other end's name, when it can be seen | `registry-1.docker.io` |
| The service, by port and protocol | 443/tcp, shown as "Secure web (HTTPS)" |
| Which side started it | docker-host contacted the registry |
| How much data moved each way | 113 KB sent, 443 MB received |
| When | per minute for recent traffic, per hour for older traffic |

It also keeps:

- **The first and last time** each machine dealt with each place, for "What's
  new". This is kept for 400 days after the last sighting, which is longer
  than the traffic history.
- **Names devices announce** for themselves on the network (mDNS and DHCP),
  and names you give them.
- **Your settings,** a salted hash of the dashboard password, and hashes of
  the tokens for browsers that are signed in.

## What is never recorded

- The contents of any connection: no web pages, requests, messages, files,
  passwords or cookies.
- Web addresses beyond the site's name. Tattle sees `example.com`, never
  `example.com/some/page`.
- Anything from inside a guest. Tattle only sees what crosses the bridge.
- Traffic between two devices on your network that involves no VM, container
  or the host.

To read names, Tattle looks at three things that travel unencrypted: DNS
answers, the server name at the start of an HTTPS or HTTP/3 connection, and
the `Host` line of a plain HTTP request. It reads those fields and discards
the packet.

## Where it is kept

- One SQLite database, `/var/lib/tattle/tattle.db`, readable only by root.
- The HTTPS certificate and its private key, in `/var/lib/tattle/tls`.
- Nothing is written anywhere else, and nothing is sent anywhere.

## How long it is kept

You choose, in Settings. By default, 90 days: the most recent 7 minute by
minute, and the rest as one total per hour. Shortening either period deletes
the older data straight away. Removing Tattle with `uninstall.sh` and
answering yes, or `apt purge tattle`, deletes everything.

## What leaves the host

Nothing.

- Tattle makes no outgoing connections. There is no telemetry, no crash
  reporting and no update check.
- The dashboard is served from the host and loads no fonts, scripts or
  images from anywhere else; the browser is told to refuse them.
- "Who owns an address" comes from a table that is installed with Tattle
  and read from disk. Addresses are not looked up over the network, and
  addresses inside your network are not looked up at all.

The only time anything Tattle-related is fetched is when you download a
release, or when someone building Tattle runs `make data` to fetch that
table from iptoasn.com.

## What someone with the dashboard password can learn

Even without contents, this is sensitive. A person who can open the
dashboard can see which sites and services each machine contacted, how
often, and at what times. If other people's devices are named on your
network, that includes the times those devices were active and which of
your services they used.

So:

- Treat the dashboard password like the Proxmox password.
- Leave HTTPS on, so the password and the data are encrypted on your
  network.
- If other people use machines or services on this host, tell them it is
  being watched and what is recorded. In some places you are legally
  required to.

## Checking these claims

Tattle is open source, and the claims on this page map to a small amount of
code:

- What is captured and parsed: `internal/decode` and `internal/tracker`.
- What is stored: the table definitions at the top of the files in
  `internal/store`.
- That nothing is sent: Tattle contains no code that opens an outgoing
  connection. You can confirm it on a running host with
  `ss -tnp | grep tattle`, which shows only connections from browsers to the
  dashboard.
