# mc-conn-poller

A lightweight daemon that probes the Minecraft proxy at the protocol level
(Java Server List Ping, Bedrock RakNet unconnected ping) from an external
vantage point and reports success to a shared
[Healthchecks.io](https://healthchecks.io) check.

Runs directly on the other hosts in the fleet — not on the Minecraft host
itself, since that's the target being monitored, and a poller running
alongside its own target can't detect a fully down target from an external
vantage point. A separate (private) aggregator daemon polls the resulting
Healthchecks.io check status and folds it into a broader health picture;
this daemon never talks to it directly.

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

## About this project

This was built with heavy Claude Code assistance — most of the implementation
is AI-generated, with the design and review driven by me. It has unit test
coverage across its poller, SLP client, and config-loading logic (see
`internal/*/*_test.go`) and runs continuously across my own production
Minecraft hosts, so it sees real day-to-day use, not just its own test
suite. Read the source and file issues if something looks off.

## Platform support

Built and tested for Gentoo Linux with OpenRC (see the OpenRC section
below). Other Linux distributions likely work — the daemon itself has no
Gentoo- or OpenRC-specific dependencies — but aren't part of this project's
support surface: service supervision, paths, and packaging assume OpenRC,
not systemd. Windows is unsupported for a build reason on top of that: its
`log/syslog` logging backend doesn't compile there (nor on Plan 9), independent
of the OpenRC-support policy above.

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

The `init.d`/`conf.d` service definitions live in a separate private
repository, not in this repo, and are deployed by my own tooling. This repo
only builds and installs the binary; write your own `openrc-run` script that
runs it with `--config`:

```shell
make install    # binary -> /usr/local/sbin
```

Copy `etc/mc-conn-poller.yaml.example` to `/usr/local/etc/mc-conn-poller.yaml`
and edit it — at least one target with a real `ping_url` is required.

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
