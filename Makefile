GO ?= go
COMPOSE := docker compose -f deployments/docker-compose.yml

.PHONY: up down test vet integration-test

up:
	$(COMPOSE) up --build -d

down:
	$(COMPOSE) down

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

integration-test:
	INTEGRATION=1 \
	DATABASE_URL=postgres://orderguard:orderguard@localhost:55432/orderguard?sslmode=disable \
	REDIS_ADDR=localhost:56379 \
	$(GO) test ./test/integration -count=1 -v
