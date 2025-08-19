FROM golang:1.25-alpine as builder
WORKDIR /src
COPY go.sum go.sum
COPY go.mod go.mod
RUN go mod download

COPY pkg pkg
COPY main.go main.go
RUN go test ./... -count=1
RUN go build -o knep

FROM gcr.io/distroless/static:nonroot
WORKDIR /app
COPY --from=builder /src/knep /app/knep
CMD ["/app/knep"]
