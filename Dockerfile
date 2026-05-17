FROM golang:1.26.3-alpine3.22 AS builder

WORKDIR /app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o note-tweet-connector ./cmd/note-tweet-connector/

FROM alpine:3.23

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/note-tweet-connector .

RUN addgroup -S app && \
    adduser -S -D -H -u 10001 -G app app && \
    mkdir -p /app/data && \
    chown -R app:app /app

USER app

EXPOSE 8080 9090

ENTRYPOINT ["./note-tweet-connector"]
CMD []
