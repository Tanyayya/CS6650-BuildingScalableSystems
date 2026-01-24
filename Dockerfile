FROM golang:1.25

WORKDIR /app

# Copy module files from H1b
COPY H1b/go.mod H1b/go.sum ./
RUN go mod download

# Copy source
COPY H1b/ ./

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -o /docker-gs-ping .

EXPOSE 8080
CMD ["/docker-gs-ping"]
