# mc-conn-poller

A lightweight daemon that probes the Minecraft proxy at the protocol level
(Java Server List Ping, Bedrock RakNet unconnected ping) from an external
vantage point and reports success to a shared
[Healthchecks.io](https://healthchecks.io) check.

Runs directly on kilo, mike, november, and papa — not on redstone, since
that's the Minecraft host being monitored. [minecraft-network-watchd](https://github.com/miikkak/minecraft-network-watchd)
polls the resulting Healthchecks.io check status and folds it into its
broader health picture; this daemon never talks to watchd directly.

On a successful handshake, the target's Healthchecks.io check is pinged. On
failure, nothing is sent — no `/fail` call. The check is shared across
pollers on multiple hosts, so an explicit fail from one poller with a
locally broken network path would incorrectly flip a check the others are
still successfully reporting on. Healthchecks.io's own silence-triggers-
alert grace-period model gives the correct "no poller anywhere succeeded"
semantics for free.

The Java SLP protocol client is duplicated from
[mc-healthcheck](https://github.com/miikkak/mc-healthcheck)'s
`internal/slp` rather than imported as a shared module — see the doc
comment on `internal/slp/client.go` for why.

## Configuration

Settings load from (highest precedence first): CLI flags, `MCCP_`-prefixed
environment variables, a YAML config file, then built-in defaults. See
[etc/mc-conn-poller.yaml.example](etc/mc-conn-poller.yaml.example) for every
key, or run `mc-conn-poller --help` for the flag equivalents. The config
file defaults to `/usr/local/etc/mc-conn-poller.yaml` (override with
`--config`).

`targets` is YAML-only — no flag or env var sets it, since a list of
structs has no sane single-flag representation. At minimum:

```yaml
targets:
  - name: proxy-java
    protocol: java
    host: play.example.com
    port: 25565
    ping_url: https://hc-ping.com/00000000-0000-0000-0000-000000000000
```

## Running

```shell
mc-conn-poller --config /usr/local/etc/mc-conn-poller.yaml
```

The daemon probes every target immediately on startup, then on its own
independent timer (`interval`, or `default_interval` if unset) until it
receives `SIGINT`/`SIGTERM`.

### OpenRC

```shell
make install                                                # binary -> /usr/local/sbin
cp etc/mc-conn-poller.yaml.example /usr/local/etc/mc-conn-poller.yaml
cp conf.d/mc-conn-poller.example /etc/conf.d/mc-conn-poller
cp init.d/mc-conn-poller /etc/init.d/mc-conn-poller
useradd -r -s /sbin/nologin -d /var/lib/mc-conn-poller mc-conn-poller
rc-update add mc-conn-poller default
rc-service mc-conn-poller start
```

Edit `/usr/local/etc/mc-conn-poller.yaml` first — at least one target with
a real `ping_url` is required.

## Building

```shell
make build
```

Produces an `mc-conn-poller` binary in the repo root. `make install`
installs it to `/usr/local/sbin` (override with `PREFIX`/`DESTDIR`).

## Testing

```shell
go test ./... -race -cover
```
