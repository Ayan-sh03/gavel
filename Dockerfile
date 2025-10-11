# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/api ./cmd/api

FROM gcr.io/distroless/base-nonroot:latest
WORKDIR /app
COPY --from=build /out/api ./api

ENV PORT=8080
EXPOSE 8080

ENTRYPOINT ["/app/api"]
