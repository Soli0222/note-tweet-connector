FROM golang:1.25.4-alpine3.22 AS builder

WORKDIR /app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o note-tweet-connector ./cmd/note-tweet-connector/

FROM alpine:3.22

RUN apk --no-cache add ca-certificates

WORKDIR /app

COPY --from=builder /app/note-tweet-connector .

EXPOSE 8080 9090

ENTRYPOINT ["./note-tweet-connector"]
CMD []