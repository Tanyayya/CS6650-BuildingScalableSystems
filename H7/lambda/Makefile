# Build the Lambda deployment zip for provided.al2 (arm64 or x86_64).
# Usage:
#   make build          – produces bootstrap.zip (x86_64)
#   make build-arm      – produces bootstrap-arm.zip (arm64, cheaper)
#   make clean

BINARY  = bootstrap
ZIPFILE = bootstrap.zip

.PHONY: build build-arm clean

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(BINARY) .
	zip $(ZIPFILE) $(BINARY)
	rm $(BINARY)
	@echo "Built $(ZIPFILE) (x86_64)"

build-arm:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -tags lambda.norpc -o $(BINARY) .
	zip bootstrap-arm.zip $(BINARY)
	rm $(BINARY)
	@echo "Built bootstrap-arm.zip (arm64)"

clean:
	rm -f $(BINARY) $(ZIPFILE) bootstrap-arm.zip
