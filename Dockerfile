FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/signin-app ./cmd/signin-app

FROM alpine:3.22

WORKDIR /app
RUN adduser -D -H signin
COPY --from=builder /out/signin-app /app/signin-app
COPY web /app/web
RUN mkdir -p /app/data && chown -R signin:signin /app

USER signin
EXPOSE 4567
ENV SIGNIN_ADDR=:4567
ENV SIGNIN_DB=/app/data/signin.db

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 CMD wget -qO- http://127.0.0.1:4567/ping || exit 1

ENTRYPOINT ["/app/signin-app"]
