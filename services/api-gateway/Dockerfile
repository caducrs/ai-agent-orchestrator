FROM golang:1.27-alpine AS build
WORKDIR /src
COPY contracts ./contracts
COPY services/api-gateway ./services/api-gateway
WORKDIR /src/services/api-gateway
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api-gateway ./cmd/api-gateway

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/api-gateway /usr/local/bin/api-gateway
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/api-gateway"]
