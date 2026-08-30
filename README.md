# mc-conn-poller

A lightweight daemon that probes the Minecraft proxy at the protocol level
(Java Server List Ping, Bedrock RakNet unconnected ping) from an external
vantage point and reports success to a shared
[Healthchecks.io](https://healthchecks.io) check.

Runs directly on kilo, mike, november, and papa — not on redstone, since
that's the Minecraft host being monitored. [minecraft-network-watchd](https://github.com/miikkak/minecraft-network-watchd)
polls the resulting Healthchecks.io check status and folds it into its
broader health picture; this daemon never talks to watchd directly.

Still being implemented — this repo currently holds only governance/CI
scaffolding, no daemon logic yet.

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
