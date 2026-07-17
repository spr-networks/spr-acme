# spr-acme

Trusted Let's Encrypt certificates for private services on an
[SPR](https://github.com/spr-networks/super) network. `spr-acme` uses the
DNS-01 challenge, so a service can stay entirely on the LAN: it needs no
public address, inbound port, or public web server.

The plugin combines three pieces:

- [go-acme/lego](https://github.com/go-acme/lego), embedded at a pinned release,
  provides automated DNS integrations for more than 200 providers.
- Certificate profiles issue and renew single-name, SAN, and wildcard
  certificates and export stable PEM paths for local services.
- A narrowly scoped SPR API keeps split-horizon hostnames in CoreDNS, mapping a
  real public-domain name such as `vault.home.example.com` to a private address.

## Important prerequisites

You must control a real public DNS domain and use a provider with an API that
can create `_acme-challenge` TXT records. A public CA cannot issue certificates
for names such as `router.lan`, `.local`, or `.internal`; the plugin rejects
those names before issuance.

DNS-01 does not require the certificate hostname to have a public A/AAAA
record. It can exist only in SPR's local DNS. Certificate names are still
submitted to public Certificate Transparency logs, so use a non-sensitive
subdomain or wildcard where appropriate.

## Installation

In the SPR UI, open **Plugins → + New Plugin** and add the repository URL for
`spr-acme`. The SPR installer creates an API token scoped only to
`/dns/hostnames:rw` and applies the `wan`, `dns`, and `api` policies to the
plugin's dedicated interface.

For a manual checkout:

```bash
cd /home/spr/super/plugins
git clone https://github.com/spr-networks/spr-acme
cd spr-acme
./install.sh
```

The plugin requires the small `/dns/hostnames` API added alongside it to SPR
core. Issuance still works against an older SPR build, but local mapping sync
will report a clear error until core is upgraded.

## First certificate

1. Open **Plugins → spr-acme → Account & DNS**.
2. Select **Staging**, enter the ACME account email, and accept the CA terms.
3. Choose the DNS provider and enter only its requested values. Prefer a token
   limited to DNS editing for the one required zone.
4. Create a profile such as `home-services` with
   `*.home.example.com` and, if needed, `home.example.com` as separate names.
5. Add optional local mappings such as
   `vault.home.example.com 192.168.2.20`, then issue the certificate.
6. Once staging succeeds, change the account CA to **Production** and issue
   again.

The UI derives provider fields directly from the embedded lego version. Blank
credential fields preserve saved values, and saved secrets are never returned
by the API.

## Using the certificates

Each profile has stable exported files under:

```text
/state/plugins/spr-acme/certificates/<profile-id>/cert.pem
/state/plugins/spr-acme/certificates/<profile-id>/chain.pem
/state/plugins/spr-acme/certificates/<profile-id>/fullchain.pem
/state/plugins/spr-acme/certificates/<profile-id>/privkey.pem
```

Mount only the profile directory into the service that terminates TLS, ideally
read-only. For example, from an SPR compose project whose `SUPERDIR` variable
points at the SPR checkout:

```yaml
services:
  reverse-proxy:
    volumes:
      - "${SUPERDIR}./state/plugins/spr-acme/certificates/home-services:/certs/home-services:ro"
```

Configure the service to read `/certs/home-services/fullchain.pem` and
`/certs/home-services/privkey.pem`. The files are replaced atomically after a
successful issuance. Services that do not watch certificate files must be
reloaded by their own supervisor; the plugin deliberately has no Docker socket
and does not restart arbitrary containers.

## Renewal and local DNS behavior

Automatic profiles are checked twice a day. lego renews only when the
certificate enters the configured renewal window (30 days by default). A
manual **Renew now** operation forces renewal.

SPR local mappings are refreshed every five minutes and immediately after a
profile change. The plugin queries only each hostname the profile owns or wants
to create, then uses compare-and-swap writes: it never fetches the complete
local mapping set, will not overwrite or delete a hostname changed outside that
profile, and reports concurrent conflicts in the UI.

## API

SPR proxies these endpoints to the backend's Unix socket:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/status` | Redacted account, certificate, renewal, and sync status |
| `GET`, `PUT` | `/account` | Read or update CA, provider, and provider credentials |
| `GET` | `/providers` | Provider codes supported by the embedded lego build |
| `GET` | `/providers/{code}` | Required and optional provider variables |
| `GET`, `POST` | `/certificates` | List statuses or create a profile |
| `PUT`, `DELETE` | `/certificates/{id}` | Update or remove a profile |
| `POST` | `/certificates/{id}/issue` | Issue or renew in the background |
| `POST` | `/certificates/{id}/sync` | Retry SPR local hostname synchronization |
| `GET` | `/certificates/{id}/log` | Read the latest redacted lego operation log |
| `GET` | `/certificates/{id}/files/{name}` | Download an exported PEM file |

The corresponding core endpoint is authenticated `GET`, `PUT`, and `DELETE`
on `/dns/hostnames/{hostname}`. Every request reads or mutates exactly one
record; writes use optional create-only and expected-value guards for
conflict-safe plugin ownership.

## Security model

- No host TCP/UDP ports are published. The UI and API use only the SPR plugin
  Unix socket.
- The container is read-only apart from its own config/state mounts and `/tmp`,
  drops every Linux capability, and uses `no-new-privileges`.
- The API token has only `/dns/hostnames:rw`; network policies grant outbound
  DNS/ACME/provider access and the SPR API, not LAN access.
- Provider secrets are stored in `config.json` with mode `0600`, passed only to
  the lego child process, omitted from API responses, and redacted from logs.
- Only environment variables documented by the selected lego provider are
  accepted. lego hook providers (`manual` and `exec`) and process-control/proxy
  variables are blocked.
- Local mapping targets must be RFC1918, IPv6 ULA, or CGNAT addresses and must
  be covered by the profile's certificate names.
- The private key is exported with mode `0600`; certificate files use `0644`
  inside a profile directory with mode `0700`.

## Reproducible build and tests

The Dockerfile builds both lego and the plugin backend from source. Image
digests, the Ubuntu snapshot, Go archives, and lego's exact release commit are
pinned in [`reproducible.env`](reproducible.env).

```bash
./test.sh
./build_docker_compose.sh --load
./update-pins.sh  # refresh upstream pins, then review git diff
```

The plugin code is MIT licensed. The embedded unmodified lego source is also
MIT licensed; see [go-acme/lego](https://github.com/go-acme/lego) for its
upstream notices and provider documentation.
