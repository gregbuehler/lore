# Build the Lore CLI and authenticated daemon HTTP API.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/lore .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 lore \
    && adduser -S -D -H -u 10001 -G lore lore \
    && mkdir -p /vault /home/lore/.cache /tmp \
    && chown -R 10001:10001 /vault /home/lore /tmp
COPY --from=build --chown=10001:10001 /out/lore /usr/local/bin/lore
ENV HOME=/home/lore
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/lore"]
CMD ["daemon", "start", "--vault", "/vault"]
