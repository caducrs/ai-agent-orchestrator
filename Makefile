MODULES=contracts services/orchestrator services/api-gateway services/llm-gateway services/agents services/agents/code-agent services/agents/log-agent services/agents/database-agent services/agents/infrastructure-agent services/demo-app

.PHONY: test race vet fmt build up down scale smoke

fmt:
	@for module in $(MODULES); do (cd $$module && go fmt ./...); done

vet:
	@for module in $(MODULES); do (cd $$module && go vet ./...); done

test:
	@for module in $(MODULES); do (cd $$module && go test ./...); done

race:
	@for module in $(MODULES); do (cd $$module && go test -race ./...); done

build:
	docker compose build

up:
	docker compose up --build -d

down:
	docker compose down

scale:
	docker compose up -d --scale code-agent=3 --scale log-agent=2 --scale database-agent=2 --scale infrastructure-agent=2

smoke:
	powershell -ExecutionPolicy Bypass -File scripts/smoke-test.ps1
