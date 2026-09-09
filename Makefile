.PHONY: dev api web test build
api:
	go run ./cmd/server
web:
	pnpm dev
dev:
	@echo "Run 'make api' and 'make web' in two terminals"
test:
	go test ./...
build:
	pnpm build
