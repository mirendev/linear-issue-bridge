.PHONY: build test lint lint-fix clean

build:
	go build -o linear-issue-bridge .

test:
	go test ./...

lint:
	golangci-lint run ./...

lint-fix:
	golangci-lint run --fix ./...

clean:
	rm -f linear-issue-bridge
