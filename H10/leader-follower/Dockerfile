
# ===== leader-follower/Dockerfile =====
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o /kvnode ./leader-follower

FROM alpine:3.19
COPY --from=builder /kvnode /kvnode
EXPOSE 8080
ENTRYPOINT ["/kvnode"]