# Testing Tattle on a real Proxmox host

Tattle has so far only run against a simulated bridge. This checklist is the
first run on real hardware (roadmap step 2). It takes about 30 minutes.

For each check, note **pass**, **fail**, or **could not test**. For a fail,
note what you expected and what the dashboard showed.

Tattle only reads traffic and config; it changes nothing on the host. The
last section removes it completely.

## 1. Install

On your computer, in the project folder:

```bash
make linux
```

```bash
scp bin/tattle-linux-amd64 root@your-proxmox:/usr/local/bin/tattle
```

```bash
scp deploy/tattle.service root@your-proxmox:/etc/systemd/system/
```

On the host:

```bash
systemctl daemon-reload && systemctl enable --now tattle
```

```bash
journalctl -u tattle -n 20 --no-pager
```

- [ ] **1a.** The log says `watching vmbr0` (or your bridges) and
      `dashboard on http://...:8477`, with no errors.
- [ ] **1b.** `http://your-proxmox:8477` opens the dashboard and it does
      **not** say "Demo data".

If the service fails to start, run `tattle` directly in a terminal on the
host instead and carry on. Send the service error; the hardening options in
the service file are the likely cause and have never been tested.

## 2. Machines

```bash
qm list; pct list
```

- [ ] **2a.** Every VM and container from that output is in the Machines
      list, with the right name and the right VM/Container label.
- [ ] **2b.** The host itself is listed, labelled "Host".
- [ ] **2c.** Templates are not listed.
- [ ] **2d.** After a minute or two, clicking a running machine shows its IP
      address under its name.

## 3. Attribution

Replace `101` with a real container ID. Each result should appear within
about five seconds, under the right machine and nowhere else.

```bash
pct exec 101 -- curl -s -o /dev/null https://example.com
```

- [ ] **3a. Container, HTTPS.** Shows `example.com`, "Secure web (HTTPS)",
      tagged "Internet".

```bash
pct exec 101 -- curl -s -o /dev/null http://example.org
```

- [ ] **3b. Container, plain HTTP.** Shows `example.org`, tagged
      "Not encrypted".

- [ ] **3c. VM.** Run the first `curl` inside a VM (over SSH or its
      console). It appears under that VM.

```bash
curl -s -o /dev/null https://www.proxmox.com
```

- [ ] **3d. Host.** Run on the host itself. It appears under the host.

- [ ] **3e. Guest with the Proxmox firewall on.** Repeat 3a in a guest whose
      network card has "Firewall" ticked. Same result, counted once.

- [ ] **3f. Tagged VLAN.** If any guest uses a VLAN tag, repeat 3a in it.

- [ ] **3g. Guest to guest.** From one guest, `ping` or `curl` another.
      It appears as **one** row, the other end is shown by its machine name,
      tagged "Inside your network".

- [ ] **3h. Incoming.** From your laptop, open something a guest serves (a
      web UI, SSH). It appears under "Coming in" with your laptop's address,
      tagged "Inside your network".

- [ ] **3i. Nothing is misattributed.** While idle, no machine shows traffic
      you know belongs to another.

## 4. Numbers

Download a file of known size, around 100 MB, inside a guest.

- [ ] **4a.** "Received" for that destination is the file size or slightly
      more (a few percent), not double.
- [ ] **4b.** The same amount is **not** also added to the host.

## 5. Local versus internet

```bash
ip -br addr
```

- [ ] **5a.** Settings → "Found on this host" lists the networks from that
      output (loopback excluded).
- [ ] **5b.** Devices on your LAN are tagged "Inside your network", and
      public sites "Internet".

## 6. Load and restarts

Run a large transfer through a guest (a big download or `iperf3`) for a
minute, with `top` open on the host.

- [ ] **6a.** Note Tattle's CPU percentage during the transfer.
- [ ] **6b.** The dashboard footer does not report missed packets. If it
      does, note the number.

```bash
systemctl restart tattle
```

- [ ] **6c.** After the restart, earlier history is still there.

## 7. Expected gaps

These are known and already on the roadmap. Note them only if they are worse
than described.

- Sites opened in a desktop browser inside a VM often show as bare addresses
  (roadmap steps 3 and 4).
- Guests behind host NAT count some traffic twice (roadmap step 5).
- Docker containers inside a VM appear as the VM.

## What to send back

Your pass/fail notes, plus the output of these. Check the output for
anything you would rather not share, such as public IP addresses, before
sending.

```bash
pveversion
```

```bash
journalctl -u tattle --no-pager | tail -50
```

```bash
curl -s localhost:8477/api/status
```

```bash
ip -br link
```

```bash
grep -vE '^\s*(#|$)' /etc/network/interfaces
```

A screenshot of the dashboard helps for anything that looked wrong.

## Remove Tattle

```bash
systemctl disable --now tattle
```

```bash
rm /etc/systemd/system/tattle.service /usr/local/bin/tattle
```

To also delete the recorded history:

```bash
rm -r /var/lib/tattle
```
