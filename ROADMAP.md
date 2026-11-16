# Roadmap to the first public release

Steps are in the order they should be done. Each one is small enough to
finish and check on its own, and each lists what "done" means.

Guiding rule: Tattle runs as root and sees every packet on the host, so
trust, accuracy and a clean install come before new features.

## Phase 0: Foundation

### 1. Repository and licence
- Initialise git, add the Apache-2.0 licence.
- Rename the Go module from `tattle` to its real GitHub path.
- Add CI that runs tests, lint and a build on every push.
- **Done when:** a fresh clone passes CI.

### 2. First run on a real Proxmox host
- Install the current build on a real host and compare against known traffic.
- Cover: a VM, a container, a guest with the Proxmox firewall enabled, a
  VLAN-aware bridge, and the host's own traffic.
- **Done when:** every case is attributed to the right machine, or has an
  issue written up. Later steps may be reordered by what this finds.

## Phase 1: Correct numbers and names

### 3. Names from split HTTPS handshakes
- Modern browsers send a handshake too large for one packet. Join the first
  packets of a connection before reading the server name.
- **Done when:** a current Chrome and Firefox both produce named connections.

### 4. Names for HTTP/3
- Read the server name from the first packet of an HTTP/3 (QUIC) connection.
- **Done when:** browsing a site over HTTP/3 shows its name, not an address.

### 5. No double counting in routed setups
- When the host forwards a guest's traffic out through another watched
  bridge, count it once, for the guest.
- **Done when:** a NAT setup shows the same totals as the guest itself reports.

### 6. Capture that keeps up
- Measure CPU and missed packets during a sustained 1 Gbit/s transfer.
- If needed, move to a kernel ring buffer so bulk data is not copied per packet.
- **Done when:** no packets are missed at 1 Gbit/s and CPU use is documented.

## Phase 2: Trust

### 7. Login
- First visit asks the owner to set a password. Sessions, logout, and a limit
  on repeated wrong guesses.
- Remove the `TATTLE_PASSWORD` environment variable.
- **Done when:** nothing but the login page is reachable without signing in.

### 8. Encrypted access
- Serve HTTPS with a generated certificate or one the owner supplies, and
  document running behind a reverse proxy.
- **Done when:** the password never travels unencrypted in a default install.

### 9. Least privilege
- Run as a dedicated user with only the capability to capture packets and
  read access to the Proxmox config. Verify the systemd restrictions on a
  real host.
- **Done when:** the service works with no root privileges beyond those two.

### 10. One place for configuration
- Remove `--local-net` and `--retention`; both move to Settings.
- Keep only the flags needed to start: where to listen, the HTTPS
  certificate, the database, the bridges, and demo mode.
- **Done when:** every option exists in exactly one place.

## Phase 3: The product

### 11. Shareable links
- Keep the selected machine, time range and view in the address bar.
- **Done when:** copying the address reopens the same view.

### 12. Detail views
- Clicking a destination shows which machines use it, when, how much, and on
  which services. Clicking a machine does the same from its side.
- Remove the "Detailed log" tab; its content lives in these views.
- **Done when:** no row is a dead end.

### 13. Simpler local/internet control
- The bar in "Where the traffic went" becomes the filter. Remove the three
  buttons under the title.
- **Done when:** there is one control for it, and it is obvious what is hidden.

### 14. What's new
- Mark the first time a machine contacts a destination, and list recent
  firsts at the top of the dashboard.
- **Done when:** a new destination is visible within seconds without searching.

### 15. Names for devices on the network
- Let the owner name any address ("Laptop"). Pick up names automatically
  where devices announce them.
- **Done when:** local addresses can be read as names everywhere they appear.

### 16. Who owns an address
- For addresses without a name, show the organisation and country from a
  database stored on the host. No lookups leave the machine.
- **Done when:** unnamed internet addresses show an owner. Needs a decision
  on which freely licensed database to ship.

### 17. Longer history
- Keep hourly summaries after the per-minute detail expires. Retention for
  both is set in Settings.
- **Done when:** a 90-day view loads quickly and the database size is bounded.

### 18. Health page
- Capture status, packets seen and missed, machines found, database size,
  version.
- **Done when:** "is it working?" can be answered from the dashboard.

## Phase 4: Release

### 19. Packaging
- A `.deb` for amd64 and arm64, built by CI, published as GitHub releases
  with checksums. Install, upgrade and uninstall tested on a real host.
- **Done when:** `apt install ./tattle.deb` gives a running, secured service.

### 20. Documentation
- README with screenshots and a two-minute install.
- A statement of what Tattle collects and that nothing leaves the host.
- Security policy, contributing guide, changelog.
- **Done when:** a stranger can install and understand it without asking.

### 21. Release 0.1.0
- Repeat step 2's checks on the packaged build, tag, publish.

## After 0.1

- Notifications for new destinations and incoming internet connections.
- Labelling destinations as advertising or telemetry.
- One view across a multi-node cluster.
- CSV export.

## Not planned

Blocking traffic, decrypting HTTPS, packet downloads, user roles, cloud
accounts and telemetry. Tattle stays a viewer.
