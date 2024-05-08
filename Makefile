# Usage:
# make              # compile all binary
# make clean        # remove ALL binaries and objects
# make test         # run tests
# make deps         # get dependencies
# make build        # compile all binary for all supported platforms
# make build-linux-amd64  # compile linux amd64 binary
# make build-linux-arm  # compile linux arm binary
# make build-linux-arm64  # compile linux arm64 binary
# make build-windows-386  # compile windows 386 binary
# make build-windows-amd64  # compile windows amd64 binary
# make build-darwin-amd64  # compile darwin amd64 binary
# make build-darwin-arm64  # compile darwin arm64 binary
# make optimize     # optimize all binary for all platforms
# make optimize-linux-amd64  # optimize linux amd64 binary
# make optimize-linux-arm  # optimize linux arm binary
# make optimize-linux-arm64  # optimize linux arm64 binary
# make optimize-windows-386  # optimize windows 386 binary
# make optimize-windows-amd64  # optimize windows amd64 binary
# make optimize-darwin-amd64  # optimize darwin amd64 binary
# make optimize-darwin-arm64  # optimize darwin arm64 binary
.PHONY = all clean build build-% optimize optimize-%

GOCMD = go
GOBUILD = $(GOCMD) build
GOCLEAN = $(GOCMD) clean
GOTEST = $(GOCMD) test
GOGET = $(GOCMD) get
BINARY_NAME = pvr
DEBUG = 0
OPTIMIZE = 1
CGO = CGO_ENABLED=0

ifeq ($(DEBUG), 1)
    BUILD_TYPE = debug
    GCFLAGS = -gcflags="all=-N -l"
    LDFLAGS = -ldflags="-extldflags=-static"
else
    BUILD_TYPE = release
    GCFLAGS = -gcflags="all=-N -l"
    LDFLAGS = -ldflags="-s -w -extldflags=-static"
endif

BUILD_DIR = build/$(BUILD_TYPE)

SUPPORTED_PLATFORMS = linux-amd64 linux-arm-6 linux-arm64-8 windows-386 windows-amd64 darwin-amd64 darwin-arm64

all: clean build

# Build the binary for all supported platforms
build:
	$(foreach platform,$(SUPPORTED_PLATFORMS), $(MAKE) build-$(platform);)

# Build the binary for a specific platform
build-%:
	$(eval GOOSARCH := $*)
	$(eval GOOS := $(firstword $(subst -, ,$(GOOSARCH))))
	$(eval GOARCH := $(word 2,$(subst -, ,$(GOOSARCH))))
	$(eval GOARM := $(word 3,$(subst -, ,$(GOOSARCH))))
	@echo ""
	@echo "building for $(GOOSARCH) with GOOS=$(GOOS) and GOARCH=$(GOARCH) and GOARM=$(GOARM)"
	$(shell export GOOS=$(GOOS); export GOARCH=$(GOARCH); export GOARM=$(GOARM); $(GOBUILD) $(GCFLAGS) $(LDFLAGS) -o $(BUILD_DIR)/$(GOOS)/$(GOARCH)/$(BINARY_NAME) -v .;)
	@if [ "$(OPTIMIZE)" = "1" ]; then make optimize-$(GOOSARCH); fi

# Optimize the binary for all platforms
optimize:
	$(foreach platform,$(SUPPORTED_PLATFORMS), $(MAKE) optimize-$(platform);)

# Optimize the binary for a specific platform
optimize-%:
	$(eval GOOSARCH := $*)
	$(eval GOOS := $(firstword $(subst -, ,$(GOOSARCH))))
	$(eval GOARCH := $(lastword $(subst -, ,$(GOOSARCH))))
	@if [ "$(GOOS)" != "darwin" ]; then upx $(BUILD_DIR)/$(GOOS)/$(GOARCH)/$(BINARY_NAME); fi

# Clean target
clean:
	rm -rf build/

# Test target
test:
	$(GOTEST) -v ./... && ./run_test.sh

# Get dependencies
deps:
	$(GOGET) -d ./...

