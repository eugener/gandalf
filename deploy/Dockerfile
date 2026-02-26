FROM golang:1.26-alpine AS builder

ARG VERSION=dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOEXPERIMENT=jsonv2 \
    go build -ldflags="-s -w -X main.version=${VERSION}" -trimpath \
    -o /gandalf ./cmd/gandalf

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /gandalf /usr/local/bin/gandalf

USER nobody
EXPOSE 8080
ENTRYPOINT ["gandalf"]
