build:
	go build -o bin/hexlet-go-crawler ./cmd/hexlet-go-crawler

test:
	go test ./...

lint:
	golangci-lint run ./...

run:
	@if [ -z "$(URL)" ]; then \
		echo "Usage: make run URL=https://example.com"; \
		exit 1; \
	fi
	go run ./cmd/hexlet-go-crawler $(URL)
