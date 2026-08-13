BIN     := bin/bettersentryio
DEV_DB  ?= postgres://localhost/bettersentryio_dev?sslmode=disable
TEST_DB ?= postgres://localhost/bettersentryio_test?sslmode=disable

.PHONY: help build run test demo fmt vet check db clean

help:
	@echo "make build   - build the binary (static, no CGO)"
	@echo "make run     - run against \$$DEV_DB with a 5s detector tick"
	@echo "make test    - run tests against \$$TEST_DB"
	@echo "make demo    - reproduce both incidents end to end"
	@echo "make check   - fmt + vet + test"
	@echo "make db      - create the local dev and test databases"

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0-dev)

build:
	CGO_ENABLED=0 go build -trimpath \
		-ldflags "-s -w -X main.version=$(VERSION)" \
		-o $(BIN) ./cmd/bettersentryio

run: build
	$(BIN) serve --database-url "$(DEV_DB)" --tick-interval 5s --base-url http://localhost:9090

test:
	BSIO_TEST_DATABASE_URL="$(TEST_DB)" go test ./...

demo:
	./scripts/demo.sh

fmt:
	gofmt -l -w .

vet:
	go vet ./...

check: fmt vet test

db:
	-createdb bettersentryio_dev
	-createdb bettersentryio_test

clean:
	rm -rf bin
