BINARY    := mc-conn-poller
CMD       := ./cmd/$(BINARY)
PREFIX    ?= /usr/local
BINDIR    := $(PREFIX)/sbin
SYSCONFDIR ?= /etc

VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS   := -s -w -X main.version=$(VERSION)
GOFLAGS   := -trimpath

.PHONY: all build install uninstall install-openrc uninstall-openrc clean

all: build

build: $(BINARY)

$(BINARY): $(shell find . -name '*.go' -not -path '*/vendor/*') go.mod go.sum
	go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BINARY) $(CMD)

install: $(BINARY)
	install -D -m 0755 $(BINARY) $(DESTDIR)$(BINDIR)/$(BINARY)

uninstall:
	rm -f $(DESTDIR)$(BINDIR)/$(BINARY)

# Installs the sample OpenRC service. An existing conf.d file is kept, since
# it is meant to be edited.
install-openrc:
	install -D -m 0755 etc/init.d/$(BINARY) $(DESTDIR)$(SYSCONFDIR)/init.d/$(BINARY)
	test -e $(DESTDIR)$(SYSCONFDIR)/conf.d/$(BINARY) || \
		install -D -m 0644 etc/conf.d/$(BINARY) $(DESTDIR)$(SYSCONFDIR)/conf.d/$(BINARY)

# Removes the init script only; the (possibly edited) conf.d file is left.
uninstall-openrc:
	rm -f $(DESTDIR)$(SYSCONFDIR)/init.d/$(BINARY)

clean:
	rm -f $(BINARY)
