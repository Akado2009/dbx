.PHONY: build test up down cli server

build: cli server

cli:
	go build -o dbx-cli ./cli

server:
	go build -o dbx-server ./server

test-unit:
	go test ./server/core/... -run "^Test(Quote|Row|Parse|Detect|Extract|Build|Normalize|Apply.*Noop|SlotName|BranchSlot)" -timeout 30s -v

test-integration:
	go test ./... -timeout 120s

test: test-unit

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f server

clean:
	docker compose down -v
	rm -f dbx-cli dbx-server
