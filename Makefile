.PHONY: generate build run migrate migrate-status migrate-down test

generate:
	mkdir -p api
	go tool oapi-codegen -config config/oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml

build:
	mkdir -p bin
	go build -o bin/trip-service ./cmd/trip-service

run: build
	./bin/trip-service -local-env

migrate:
	go run ./scripts/migrate up

migrate-status:
	go run ./scripts/migrate status

migrate-down:
	go run ./scripts/migrate down

test:
	go test -race ./...
