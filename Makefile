.PHONY: build test tidy up down

build:
	go build -o bin/pgctl ./cmd/pgctl

test:
	go test ./...

tidy:
	go mod tidy

up:
	docker compose up -d pg-old pg-new

down:
	docker compose down -v
