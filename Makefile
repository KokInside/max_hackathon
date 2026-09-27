.PHONY: rebuild
rebuild:
	docker compose up --build -d

.PHONY: up
up:
	docker compose up -d

.PHONY: stop
stop:
	docker compose stop

.PHONY: down
down:
	docker compose down