VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build web linux demo data test lint clean

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
# iptoasn.com). It is bundled with releases and is not kept in git.
data:
	mkdir -p data
	curl -fL --proto '=https' -o data/ip2asn.tsv.gz https://iptoasn.com/data/ip2asn-combined.tsv.gz

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

clean:
	rm -rf bin internal/ui/dist
