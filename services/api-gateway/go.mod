module github.com/caduc/ai-agent-orchestrator/services/api-gateway

go 1.27.0

require (
	github.com/caduc/ai-agent-orchestrator/contracts v0.0.0
	github.com/redis/go-redis/v9 v9.7.1
	google.golang.org/grpc v1.70.0
	google.golang.org/protobuf v1.36.5
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	go.opentelemetry.io/otel/metric v1.34.0 // indirect
	go.opentelemetry.io/otel/trace v1.34.0 // indirect
	golang.org/x/net v0.34.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	golang.org/x/text v0.21.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250115164207-1a7da9e5054f // indirect
)

replace github.com/caduc/ai-agent-orchestrator/contracts => ../../contracts
