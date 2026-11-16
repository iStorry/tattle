# Security

Tattle runs as root on a hypervisor and reads every packet that crosses its
bridges. A flaw in it matters, so reports are welcome and taken seriously.

## Reporting a vulnerability

Please report it privately, not in a public issue:

1. Go to the repository's **Security** tab on GitHub.
2. Choose **Report a vulnerability**.

That opens a private conversation with the maintainers. Say what you found,
how to reproduce it, and which version you were running (`tattle --version`).
You should hear back within a week.

Please give us a reasonable chance to release a fix before you describe the
problem publicly.

## Which versions get fixes

The latest release. Tattle is young, and older releases are not maintained.

## What counts

Anything that lets someone do more than they should. For example:

- Seeing traffic history, settings or device names without signing in.
- Getting past the limit on password or setup-code guesses.
- Making Tattle crash, hang, use unbounded memory, or misbehave by sending
  crafted packets across a bridge it watches. Every packet on the bridge is
  parsed by code in `internal/decode`, as root.
- Getting text from the network (a hostname, a device name) to do anything
  in the dashboard other than be displayed.
- Reading the private key, the password hash or session tokens.
- Making Tattle send anything off the host.

## What Tattle protects, and what it does not

It helps to know what the design intends.

**Intended:**

- Nothing but the sign-in page is reachable without the password. Setting
  the first password needs a code that is only written to the host's log.
- Passwords are stored as PBKDF2 hashes, sessions as hashes of random
  tokens in a cookie that page scripts cannot read.
- The dashboard is served over HTTPS by default.
- Names taken from the network are restricted to hostname characters before
  they are stored.

**Not protected against:**

- **Someone with root on the host.** They can read the database and the
  private key, and reset the password. That is by design: root is the owner.
- **Someone with the dashboard password.** There is one password and no
  roles. Anyone who has it can read everything and change the settings.
- **A device lying about its name.** Announced device names are shown as
  given. Any device on your network can call itself "router".
- **A certificate warning you click through without checking.** With the
  generated certificate, compare the fingerprint in the log on first visit.

## A note on what Tattle is

Tattle is a viewer. It does not block traffic, decrypt connections or
detect intrusions, and should not be relied on as a security control.
