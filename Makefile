.PHONY: generate build run migrate migrate-status migrate-down test test-integration lint

generate:
	mkdir -p api
	go tool oapi-codegen -config config/oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml
	go generate ./internal/usecase/... ./internal/http/... ./internal/infra/telemetry/...

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
	mkdir -p bin
	go test -race -count=2 -coverprofile=bin/coverage.out -coverpkg=./internal/domain/...,./internal/usecase/... ./...
	go tool cover -func=bin/coverage.out

test-integration:
	go test -tags=integration -race -count=2 -timeout=6m -v ./internal/repository/trip

lint:
	golangci-lint run ./...
	golangci-lint run --build-tags=integration ./...
