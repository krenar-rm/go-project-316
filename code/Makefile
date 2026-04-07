GOCACHE_DIR := $(CURDIR)/.gocache
GOENV := GOCACHE=$(GOCACHE_DIR)

setup:
	$(GOENV) go mod download

build:
	$(GOENV) go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler

test:
	$(GOENV) go test ./...

lint:
	golangci-lint run ./...

run:
	@if [ -z "$(URL)" ]; then \
		echo "Usage: make run URL=https://example.com"; \
		exit 1; \
	fi
	$(GOENV) go run ./cmd/hexlet-go-crawler $(URL)

clean:
	rm -rf $(GOCACHE_DIR)
