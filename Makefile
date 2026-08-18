.DEFAULT_GOAL := default

BINARY_NAME := reduce-pdf-size
TMP_DIR := tmp
WEB_DIR := web
WEB_PORT := 8080

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

LDFLAGS := -s -w

$(TMP_DIR):
	@mkdir -p $(TMP_DIR)

build: $(TMP_DIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o $(TMP_DIR)/$(BINARY_NAME) ./cmd/reduce-pdf-size

build-all: $(TMP_DIR)
	$(foreach PLATFORM,$(PLATFORMS), \
		$(eval GOOS := $(word 1,$(subst /, ,$(PLATFORM)))) \
		$(eval GOARCH := $(word 2,$(subst /, ,$(PLATFORM)))) \
		$(eval EXT := $(if $(filter windows,$(GOOS)),.exe,)) \
		CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) go build -trimpath -ldflags="$(LDFLAGS)" -o $(TMP_DIR)/$(BINARY_NAME)-$(GOOS)-$(GOARCH)$(EXT) ./cmd/reduce-pdf-size; \
	)

test: $(TMP_DIR)
	go test ./... -coverprofile=$(TMP_DIR)/coverage.out
	go tool cover -func=$(TMP_DIR)/coverage.out

web-build:
	CGO_ENABLED=0 GOOS=js GOARCH=wasm go build -trimpath -ldflags="$(LDFLAGS)" -o $(WEB_DIR)/$(BINARY_NAME).wasm ./cmd/$(BINARY_NAME)-wasm
	@GOROOT=$$(go env GOROOT); \
	for candidate in "$$GOROOT/lib/wasm/wasm_exec.js" "$$GOROOT/misc/wasm/wasm_exec.js"; do \
		if [ -f "$$candidate" ]; then \
			cp "$$candidate" $(WEB_DIR)/wasm_exec.js; \
			exit 0; \
		fi; \
	done; \
	echo "wasm_exec.js not found under $$GOROOT" >&2; exit 1

web-serve: web-build
	@echo "Serving $(WEB_DIR)/ at http://localhost:$(WEB_PORT) (Ctrl+C to stop)"
	cd $(WEB_DIR) && python3 -m http.server $(WEB_PORT)

fmt:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "The following files are not gofmt'd:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

clean:
	@rm -rf $(TMP_DIR)
	@rm -f $(WEB_DIR)/$(BINARY_NAME).wasm $(WEB_DIR)/wasm_exec.js

default: build

.PHONY: build build-all test fmt web-build web-serve clean default