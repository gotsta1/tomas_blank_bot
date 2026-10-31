.PHONY: test build up

test:
	cd backend && go test -race ./... && go vet ./...
	cd frontend && npm run build

build:
	docker compose build

up:
	docker compose up -d --build
