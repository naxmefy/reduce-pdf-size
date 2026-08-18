.DEFAULT_GOAL := default

BINARY_NAME := reduce-pdf-size
TMP_DIR := tmp

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

$(TMP_DIR):
	@mkdir -p $(TMP_DIR)

build: $(TMP_DIR)
	CGO_ENABLED=0 go build -o $(TMP_DIR)/$(BINARY_NAME) ./cmd/reduce-pdf-size

build-all: $(TMP_DIR)
	$(foreach PLATFORM,$(PLATFORMS), \
		$(eval GOOS := $(word 1,$(subst /, ,$(PLATFORM)))) \
		$(eval GOARCH := $(word 2,$(subst /, ,$(PLATFORM)))) \
		$(eval EXT := $(if $(filter windows,$(GOOS)),.exe,)) \
		CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -o $(TMP_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT) ./cmd/reduce-pdf-size; \
	)

test: $(TMP_DIR)
	go test ./... -coverprofile=$(TMP_DIR)/coverage.out
	go tool cover -func=$(TMP_DIR)/coverage.out

fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt'd:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

clean:
	@rm -rf $(TMP_DIR)

default: build

.PHONY: build build-all test fmt clean default