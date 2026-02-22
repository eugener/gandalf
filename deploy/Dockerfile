FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /gandalf ./cmd/gandalf

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /gandalf /usr/local/bin/gandalf

EXPOSE 8080
ENTRYPOINT ["gandalf"]
