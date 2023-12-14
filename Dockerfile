FROM golang:1.21-alpine as builder
WORKDIR /src
COPY go.sum go.sum
COPY go.mod go.mod
RUN go mod download
COPY . .
RUN go build -o knep

FROM alpine:3
WORKDIR /app
COPY --from=builder /src/knep /app/knep
CMD ["/app/knep"]