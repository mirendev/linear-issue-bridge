.PHONY: build test lint lint-fix clean dev

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

clean:
	rm -f linear-issue-bridge
