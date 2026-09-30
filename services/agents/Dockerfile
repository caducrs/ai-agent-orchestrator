FROM golang:1.27-alpine AS build
ARG AGENT
WORKDIR /src
COPY contracts ./contracts
COPY services/agents ./services/agents
WORKDIR /src/services/agents/${AGENT}
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agent ./cmd/${AGENT}

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/agent /usr/local/bin/agent
USER 10001:10001
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/agent"]
