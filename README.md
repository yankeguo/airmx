# airmx

A personal MX server written in Go. It receives mail over SMTP, verifies
SPF / DKIM / DMARC, delivers each message to the inbox or the spam folder
according to your policy (or rejects it outright during the SMTP session),
and serves a small web UI for reading and managing mail.

## Features

- Inbound SMTP only (built on go-smtp) — receives, never relays
- Recipient allowlist: unknown `RCPT TO` addresses are rejected at SMTP time
  with `550`; `*@domain` wildcards supported
- SPF (fail and softfail handled separately), DKIM and DMARC verification,
  recorded in an `Authentication-Results` header on every stored message
- Configurable policy per check: `reject` (`554` after DATA), `spam`, or `inbox`
- Maildir storage: `<data_dir>/<recipient>/{tmp,new,cur}` — the spam
  classification lives in the injected `X-Spam-Status` header, so no folder
  split is needed
- Web UI (Chinese and English, following the browser language with a manual
  switch in the nav) with a login page and encrypted cookie sessions
  (automatic `Secure` flag behind TLS-terminating proxies):
  - light/dark theme following the system color scheme
  - unified message listing (inbox and spam merged) with unread markers and
    spam badges
  - plain-text-first viewing: messages without a text/plain part get one
    derived from the HTML; the HTML version (and its remote images, i.e.
    tracking pixels) is only loaded after explicitly switching to the HTML
    tab, where it renders in a sandboxed, CSP-restricted iframe
  - attachment download and message deletion
- Single static binary; templates and CSS embedded with `go:embed`

## Build

```sh
go build -ldflags "-X main.assetVersion=$(git rev-parse --short HEAD)" -o airmx .
```

The `assetVersion` stamp ends up in static asset URLs (`?v=<revision>`) for
cache busting; without it the server falls back to its start time, so plain
`go build` works too.

## Configure

```sh
cp config.example.yaml config.yaml
# Generate a bcrypt hash for the web password:
./airmx hashpw 'your-password'
```

Paste the resulting hash into `web.password_bcrypt`. See
`config.example.yaml` for all available options.

## Run

```sh
./airmx -config config.yaml
```

Binding to port 25 requires root or
`setcap 'cap_net_bind_service=+ep' airmx`. Only **TCP 25** needs to be
reachable from the internet (SMTP runs over TCP); outbound DNS (UDP/TCP 53)
must work for SPF/DKIM/DMARC lookups.

## DNS requirements

- An MX record for your domain pointing at this server's hostname
- A/AAAA records for that hostname pointing at this server's IP
- Recommended: a PTR record for the IP, and an SPF record for your own domain

## Deploy with systemd

The repo ships `airmx.service`, which waits for `network-online.target` and
applies basic hardening (dedicated user, `CAP_NET_BIND_SERVICE`,
`ProtectSystem=strict`, …):

```sh
sudo install -m755 airmx /usr/local/bin/
sudo useradd -r -d /var/lib/airmx airmx
sudo install -d -o airmx -g airmx /var/lib/airmx
sudo install -d /etc/airmx
# Owned by the airmx user (the service runs unprivileged); 0600 keeps the
# bcrypt hash and any secrets private.
sudo install -m600 -o airmx -g airmx config.yaml /etc/airmx/config.yaml   # data_dir: /var/lib/airmx/data
sudo install -m644 airmx.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now airmx
```

## Deploy with Docker

Every push builds `ghcr.io/yankeguo/airmx` (`latest` on the default branch,
plus per-branch and per-commit tags). The image contains **no default config
file and no data directory** — you must mount your own config at
`/etc/airmx/config.yaml`, and set `data_dir` to a path you also mount:

```sh
docker run -d --name airmx \
  -p 25:25 -p 8080:8080 \
  -v /path/to/config.yaml:/etc/airmx/config.yaml:ro \
  -v airmx-data:/data \
  ghcr.io/yankeguo/airmx:latest   # data_dir: /data
```

## STARTTLS (optional)

Set `tls_cert_dir` to a directory containing `<domain>.crt` and
`<domain>.key` (PEM; ECDSA and RSA both work) to advertise STARTTLS on the
SMTP port. The certificate is loaded at startup and reloaded automatically
when the files change or approach expiry, so you can point it at a
directory maintained by an external ACME client. For example, with Caddy
issuing and renewing the certificate, mount its storage into the container:

```sh
docker run -d --name airmx \
  -p 25:25 -p 8080:8080 \
  -v /path/to/config.yaml:/etc/airmx/config.yaml:ro \
  -v airmx-data:/data \
  -v /var/lib/caddy/.local/share/caddy/certificates:/etc/airmx/certs:ro \
  ghcr.io/yankeguo/airmx:latest   # tls_cert_dir: /etc/airmx/certs/<acme-issuer-dir>
```

Note that Caddy stores certificates one level deeper
(`.../certificates/<issuer>/<domain>/<domain>.crt`), so mount or symlink
accordingly — airmx expects the `.crt`/`.key` pair directly inside
`tls_cert_dir`.

## Web Push notifications (optional)

The web UI can push a browser notification whenever a message is delivered,
even when no tab is open (the browser's own push connection delivers it;
iOS requires the page to be installed to the home screen). Setup:

```sh
./airmx genpushkey
```

Paste the printed key pair into `web.push` in your config, restart, open
the web UI **over HTTPS** (push requires a secure context — use the reverse
proxy setup above), and click 打开 Web 通知 / Enable notifications on the
message list. Each
browser you enable it on registers a subscription, stored in
`<data_dir>/push_subscriptions.json`; dead endpoints are pruned
automatically on the next delivery.

## Reverse proxy (optional)

To serve the web UI over HTTPS, point your proxy at `web_listen`
(`127.0.0.1:8080` by default). With Caddy:

```caddy
mail.example.com {
	reverse_proxy 127.0.0.1:8080
}
```

Caddy sets `X-Forwarded-Proto` automatically, so session cookies get the
`Secure` flag without extra configuration.

## Local testing

You can exercise the full pipeline without touching port 25. Set
`smtp_listen: ":2525"` and send a test message with swaks or any SMTP
client:

```sh
swaks --server 127.0.0.1:2525 --from alice@example.com --to me@example.com \
      --header "Subject: test" --body "hello"
```

Then open `http://127.0.0.1:8080/` and log in. Note that SPF will usually be
`none`/error when testing from localhost — that is expected, since
`spf_fail` / `spf_softfail` only match explicit `-all` / `~all` results.

## Scope

No outbound SMTP, no IMAP/POP3 — receive mail, read it in the browser.
