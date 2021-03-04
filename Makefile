.PHONY: build gofmt

build:
	go vet titlebot.go
	go build titlebot.go

gofmt:
	gofmt -s -w titlebot.go
