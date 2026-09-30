FROM golang:1.27-alpine AS build
WORKDIR /src
COPY services/demo-app ./services/demo-app
WORKDIR /src/services/demo-app
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/demo-app ./cmd/demo-app

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 app && adduser -S -D -H -u 10001 -G app app
COPY --from=build /out/demo-app /usr/local/bin/demo-app
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/demo-app"]
