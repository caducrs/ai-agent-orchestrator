FROM golang:1.27-alpine AS build
WORKDIR /src
COPY contracts ./contracts
COPY services/llm-gateway ./services/llm-gateway
WORKDIR /src/services/llm-gateway
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llm-gateway ./cmd/llm-gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/llm-gateway /usr/local/bin/llm-gateway
USER 10001:10001
EXPOSE 9091
ENTRYPOINT ["/usr/local/bin/llm-gateway"]
