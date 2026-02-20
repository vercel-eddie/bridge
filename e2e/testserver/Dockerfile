FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY main.go .
RUN go mod init testserver && \
    CGO_ENABLED=0 go build -ldflags="-s -w" -o /testserver .

FROM alpine:3.21

COPY --from=builder /testserver /usr/local/bin/testserver

ENTRYPOINT ["testserver"]
