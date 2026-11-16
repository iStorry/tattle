VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

# A package version must start with a digit: v0.1.0 becomes 0.1.0, and an
# untagged build sorts before any release.
DEB_VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null | sed 's/^v//; s/-/~/' | grep . || echo 0.0.0~git$$(git rev-parse --short HEAD 2>/dev/null || echo dev))

.PHONY: build web linux demo data deb test lint clean

# Build for this computer.
build: web
	go build -ldflags "$(LDFLAGS)" -o bin/tattle ./cmd/tattle

# Build the dashboard into internal/ui/dist, which the binary embeds.
web:
	cd web && pnpm install --frozen-lockfile && pnpm build

# Build the binaries that go on a Proxmox host.
linux: web
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/tattle-linux-amd64 ./cmd/tattle
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o bin/tattle-linux-arm64 ./cmd/tattle

# Fetch the table of who owns which internet addresses (public domain, from
# iptoasn.com). It is bundled with releases and is not kept in git. The file
# keeps the date it was published, which the Health page reports.
data:
	mkdir -p data
	curl -fL -R --proto '=https' -o data/ip2asn.tsv.gz https://iptoasn.com/data/ip2asn-combined.tsv.gz

# Build the .deb packages into dist/. Needs dpkg-deb, which is on Debian and
# Ubuntu; elsewhere, run the build script inside a debian container.
deb: linux
	@test -f data/ip2asn.tsv.gz || $(MAKE) data
	packaging/build-deb.sh $(DEB_VERSION) amd64
	packaging/build-deb.sh $(DEB_VERSION) arm64

# Try the dashboard with invented traffic.
demo: build
	./bin/tattle --demo --listen 127.0.0.1:8477

# The Go code embeds the built dashboard, so it is built first.
test: web
	go vet ./cmd/... ./internal/...
	GOOS=linux go vet ./cmd/... ./internal/...
	go test ./internal/...

lint: web
	@test -z "$$(gofmt -l cmd internal)" || { echo "gofmt needed on:"; gofmt -l cmd internal; exit 1; }
	cd web && pnpm exec oxlint --deny-warnings
	@if command -v shellcheck >/dev/null; then \
		shellcheck install.sh uninstall.sh packaging/build-deb.sh packaging/test-deb.sh packaging/postinst packaging/prerm packaging/postrm; \
	else \
		echo "shellcheck is not installed; the shell scripts were not checked"; \
	fi

clean:
	rm -rf bin dist internal/ui/dist
