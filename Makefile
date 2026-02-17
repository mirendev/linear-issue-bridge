.PHONY: build test lint lint-fix clean dev backfill

build:
	go build -o linear-issue-bridge .

test:
	go test ./...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

dev:
	go run .

backfill:
	go run ./cmd/backfill $(ARGS)

clean:
	rm -f linear-issue-bridge
