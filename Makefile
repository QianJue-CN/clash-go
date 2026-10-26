.PHONY: build run test check

build:
	go build -buildvcs=false -trimpath -o build/clash-go .

run:
	go run -buildvcs=false .

test:
	go test -buildvcs=false -race ./...

check:
	go vet -buildvcs=false ./...
	go test -buildvcs=false -race ./...
