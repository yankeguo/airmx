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
- Web UI with a login page and encrypted cookie sessions (automatic `Secure`
  flag behind TLS-terminating proxies):
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
go build -o airmx .
```

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
