default: test

build:
	go build -v ./...

install:
	go install -v ./...

lint:
	golangci-lint run

generate:
	go generate ./...

# Unit tests: in-memory fake API, no credentials.
test:
	go test -race -count=1 ./...

# Acceptance tests against the real SpinupWP API. Creates and deletes a site on SPINUPWP_ACC_SERVER.
testacc:
	TF_ACC=1 go test -count=1 -v -timeout 60m ./...

.PHONY: default build install lint generate test testacc
