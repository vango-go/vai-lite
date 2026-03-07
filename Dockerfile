FROM golang:1.25 AS build

WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal
COPY pkg ./pkg
COPY public ./public

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/vai-lite-beta ./cmd/server

FROM debian:bookworm-slim

RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates tzdata \
	&& rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/vai-lite-beta /app/vai-lite-beta
COPY public /app/public

ENV APP_ADDR=:8080
ENV APP_BASE_URL=https://vai-lite-beta.fly.dev

EXPOSE 8080

CMD ["/app/vai-lite-beta"]
