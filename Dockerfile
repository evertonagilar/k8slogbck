FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY main.go go.mod go.sum /app/
RUN go build -o k8slogbck main.go

FROM alpine
RUN apk add --no-cache ca-certificates
COPY --from=builder /app/k8slogbck /usr/local/bin/k8slogbck
ENTRYPOINT ["/usr/local/bin/k8slogbck"]
