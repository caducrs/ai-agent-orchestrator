FROM golang:1.27-alpine AS build
WORKDIR /src
COPY contracts ./contracts
COPY services/orchestrator ./services/orchestrator
WORKDIR /src/services/orchestrator
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/orchestrator ./cmd/orchestrator

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/orchestrator /usr/local/bin/orchestrator
USER 10001:10001
EXPOSE 9090
ENTRYPOINT ["/usr/local/bin/orchestrator"]
