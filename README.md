# mc-conn-poller

A lightweight daemon that probes the Minecraft proxy at the protocol level
(Java Server List Ping, Bedrock NetherNet signaling / RakNet unconnected ping) from an external
vantage point and reports success to a shared
[Healthchecks.io](https://healthchecks.io) check (and, optionally, an Uptime Kuma push monitor).

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

### Uptime Kuma

A target can also (or instead) report to an [Uptime Kuma](https://github.com/louislam/uptime-kuma)
**Push** monitor by setting `kuma_push_url` to the monitor's push URL. At
least one of `ping_url` and `kuma_push_url` is required; with both set, each
successful probe is reported to both, in parallel, and one failing does not
affect the other.

```yaml
targets:
  - name: proxy-java
    protocol: java
    host: play.example.com
    port: 25565
    kuma_push_url: https://kuma.example.com/api/push/<token>
```

On success the poller sends `GET <kuma_push_url>?status=up&msg=OK&ping=<ms>`,
where `ping` is the probe latency. A URL pasted straight from Kuma (with its
`?status=up&msg=OK&ping=` suffix) works: those three parameters are
overridden and any others are kept. Like `ping_url`, the push URL is a
bearer secret and is kept out of logs and errors.

By default nothing is sent on failure, as with Healthchecks.io. Kuma marks
the push monitor DOWN when no push arrives within its heartbeat interval,
so:

- Set the monitor's **Heartbeat Interval** above the target's `interval`,
  and above `2 × interval` unless `ip_family` is pinned: with the default
  `alternate` each family pushes only every second round.
- Several pollers may share one push monitor; any one of them reporting
  keeps it UP.

#### Reporting failures (`kuma_report_failure`)

Success-only pushes leave Kuma unable to tell "server down" from "poller
down", the alert carries no reason, and detection takes up to a heartbeat
interval. Setting `kuma_report_failure: true` (default `false`; requires
`kuma_push_url`) makes the poller push
`GET <kuma_push_url>?status=down&msg=<reason>` as soon as a probe fails, so
Kuma alerts immediately with the probe error (for example
`connection refused` or `i/o timeout`) as the message.

This is the one place the poller reports a failure, and it is deliberately
narrow: it is opt-in, it goes to the Kuma push monitor only (a Kuma monitor
is normally per poller or per target), and **never to Healthchecks.io**,
whose check is shared across pollers. A probe aborted by daemon shutdown is
not reported. The message is the probe error on one line, truncated to 200
characters, with any configured `ping_url` / `kuma_push_url` redacted; it
names the monitored host and port, so keep it in mind if Kuma heartbeat
messages are visible to others.

Caveats:

- **Shared monitor:** if several pollers share one push monitor, one
  poller's `down` flips it while the others report `up`. Use one monitor per
  poller, or leave this option off.
- **Alternating address families:** with `ip_family` unset, a failure on one
  family while the other succeeds flips the monitor up/down every round.
  Pin `ip_family` (one target per family) when this option is on.
- A `down` push is still a request to Kuma. If Kuma is unreachable, its
  heartbeat timeout remains the fallback.

**Replacing GameDig monitors:** create one Push monitor per Java/Bedrock
entrypoint in Kuma, put its push URL in the matching target's
`kuma_push_url`, then delete the GameDig monitor. The poller's Bedrock
`auto` transport covers NetherNet, which GameDig's RakNet check does not.

### Timeouts

`timeout` bounds the probe and `ping_timeout` (default `5s`) bounds the
Healthchecks.io ping that follows a successful probe. Rounds run
sequentially, so a round can take up to `timeout + ping_timeout`, and
validation requires that to be less than `interval` (otherwise ticks are
dropped and pings arrive less often than configured). A `bedrock` target in
`auto` transport (below) makes up to two probe attempts, so its bound is
`2 × timeout + ping_timeout`.

### Bedrock transport

Bedrock clients from 26.60 on speak only NetherNet (WebRTC), and Geyser can
drop RakNet. A `bedrock` target therefore takes a `transport`:

| `transport`      | Probe                                                                                       |
| ---------------- | ------------------------------------------------------------------------------------------- |
| `auto` (default) | NetherNet first; on failure, RakNet. A RakNet-only success is logged as a warning.          |
| `nethernet`      | `GET /v1/join` on the TCP signaling port; needs HTTP 200 and a JSON object with `protocol`. |
| `raknet`         | UDP unconnected ping, as before.                                                            |

In `auto` mode `timeout` applies to each attempt, so one round can take up to
twice `timeout` when NetherNet times out before the RakNet fallback runs.

The NetherNet check confirms the proxy is up and its signaling is reachable;
it does not exercise the UDP gameplay leg (that needs a full WebRTC
handshake). `signaling_scheme` (`https` by default, or `http`) selects the
scheme for the signaling request. With `ip_family` pinned, the TCP request
is pinned to that family too.

### IP family

Each probe is pinned to a single IP address family, because Go's default
dialer races IPv4 and IPv6 and reports success if either connects, which
would hide an outage confined to one family. A target's `ip_family` chooses
which:

- `alternate` (default): IPv4 and IPv6 alternate across rounds. Each family
  is probed (and pings) only every second round, so Healthchecks.io's period
  and grace for that check must allow for `2 × interval`, and a single-family
  outage is not distinguishable from a healthy target by silence alone.
- `ipv4` / `ipv6`: every round uses that family. Use this for single-stack
  targets or pollers (an IPv4 literal `host`, or a host without IPv6 routing,
  otherwise fails every second round). To alert on each family independently,
  configure one pinned target per family, each with its own Healthchecks.io
  check; see `etc/mc-conn-poller.yaml.example`.

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
and edit it — at least one target with a real `ping_url` or `kuma_push_url`
is required.

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
