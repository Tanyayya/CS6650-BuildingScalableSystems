FROM alpine:3.19
RUN apk --no-cache add ca-certificates wget
COPY server /server
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1
ENTRYPOINT ["/server"]