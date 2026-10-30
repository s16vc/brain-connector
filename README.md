# brain-connector

`brain-connector` lets Brain reach a team's database from inside the team's network. It runs next to the database, opens one outbound TLS WebSocket to BrainData (`wss://data.team-brain.com`), and carries Brain's database connections over that socket. The network needs no inbound port, no public address for the database and no VPN.

It reaches exactly one address, the `--target` it was started with, and refuses every other address BrainData asks for.

## Install

With Docker:

```bash
docker run -d --restart unless-stopped \
  -e BRAIN_CONNECTOR_TOKEN=bdc_... \
  ghcr.io/s16vc/connector --target db.internal:5432
```

The image is distroless, runs as a non-root user and is published for `linux/amd64` and `linux/arm64`. `:latest` follows the newest stable release; pin a version tag such as `:0.2.0` to stay on one.

Or download a binary from [Releases](https://github.com/s16vc/brain-connector/releases): `brain-connector-linux-amd64`, `brain-connector-linux-arm64`, `brain-connector-windows-amd64.exe` or `brain-connector-darwin-arm64`, plus `SHA256SUMS`. Check the download against `SHA256SUMS` before running it:

```bash
sha256sum --check --ignore-missing SHA256SUMS          # Linux
grep darwin-arm64 SHA256SUMS | shasum -a 256 --check  # macOS
chmod +x brain-connector-linux-amd64
BRAIN_CONNECTOR_TOKEN=bdc_... ./brain-connector-linux-amd64 --target db.internal:5432
```

On Windows, compare `(Get-FileHash brain-connector-windows-amd64.exe -Algorithm SHA256).Hash` with its line in `SHA256SUMS`.

## Flags and environment

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--token` | `BRAIN_CONNECTOR_TOKEN` | required | The connector token from Brain |
| `--target` | `BRAIN_CONNECTOR_TARGET` | required | The database `host:port` this connector may reach |
| `--server` | `BRAIN_CONNECTOR_SERVER` | `wss://data.team-brain.com` | BrainData address |
| `--insecure` | | `false` | Allows a `ws://` server address, for local testing only |
| `--version` | | | Prints the version and exits |

A flag wins over its environment variable. Prefer the environment variable for the token: a flag is visible in the process list.

The connector logs JSON lines to stdout about the tunnel's state and refused addresses. It never logs the token or any data that passes through it.

## What it can reach

Only `--target`. For every connection BrainData asks for an address; the connector compares it with `--target` (host case-insensitive, port as a number) and refuses anything else without dialling it. The host in `--target` is resolved and dialled from where the connector runs, so a name that only resolves inside the network works.

The connector never accepts inbound connections and never listens on a port. To limit what it can see further, run it on a network segment that can reach the database and nothing else.

## Tokens

Brain mints the token on the database connection's page and shows it once. It is signed by Brain; the connector only checks that it is well formed and sends it to BrainData, which verifies the signature before opening the tunnel. A token works for one connection in one workspace.

Rotating the token on the connection page issues a new one and cuts the old connector off: from then on Brain routes that connection only to a connector holding the new token, so the old one carries nothing. Stop the old connector and start it again with the new token.

## Two connectors on one token

One token holds one tunnel. When a second connector connects with the same token, BrainData keeps the newer tunnel and closes the older one with the reason `replaced by another connector with the same token`. The connector that was replaced logs that sentence and waits 5 minutes before it connects again, so two copies started with one token take turns every 5 minutes instead of evicting each other every second. Run one connector per token.

After any other disconnect the connector reconnects on its own: the first retry comes within a second, and the wait doubles up to a minute, with jitter.

## Build and test

```bash
make build                    # bin/brain-connector
make test                     # go test ./...
make release VERSION=v0.2.0   # dist/: four binaries and SHA256SUMS
make image VERSION=v0.2.0     # local image
```

A tag `vX.Y.Z` (or `vX.Y.Z-rc.N` for a pre-release) runs the tests, publishes the binaries and `SHA256SUMS` as a GitHub release, and pushes `ghcr.io/s16vc/connector:X.Y.Z`. A stable version also moves `:latest`; a pre-release does not.

## Packages

- `protocol`: the wire format shared with BrainData: the stream header, reply statuses, the close code for a replaced connector, and the structural check of a connector token.
- `agent`: the connector itself.
- `cmd/brain-connector`: the binary.

## License

Apache License 2.0 with the Commons Clause condition (see `LICENSE`). You may
run brain-connector for any purpose, including inside your business, change it
and share it. You may not sell it: no charging for brain-connector itself, or for
a product or service whose value comes entirely or substantially from it.
