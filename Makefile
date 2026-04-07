setup:
	cd code && go mod download

test:
	go test -v ./tests

lint:
	golangci-lint run tests/... code/...
