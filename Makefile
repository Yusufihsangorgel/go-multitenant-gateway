.PHONY: run build test vet tidy

run:
	go run ./cmd/gateway

build:
	go build -o gateway ./cmd/gateway

test:
	go test ./...

vet:
	go vet ./...

tidy:
	go mod tidy
