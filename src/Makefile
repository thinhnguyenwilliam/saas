.PHONY: infra-up infra-down run-sub run-order tidy

infra-up:
	docker compose up -d

infra-down:
	docker compose down -v

tidy:
	cd subscription-service && go mod tidy
	cd order-service && go mod tidy

run-sub:
	cd subscription-service && go run main.go

run-order:
	cd order-service && go run main.go
