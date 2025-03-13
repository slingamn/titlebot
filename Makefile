.PHONY: build gofmt

# disable linking against native libc / libpthread by default;
# this can be overridden by passing CGO_ENABLED=1 to make
export CGO_ENABLED ?= 0

build:
	go test .
	go vet .
	go build titlebot.go

gofmt:
	gofmt -s -w *.go
