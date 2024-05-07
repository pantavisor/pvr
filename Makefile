# Usage:
# make        # compile all binary
# make clean  # remove ALL binaries and objects

.PHONY = all clean

all: clean build optimize

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -extldflags=-static" -o build/linux_amd64/pvr
	CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=5 go build -ldflags="-s -w -extldflags=-static" -o build/linux_arm/pvr
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 GOARM=5 go build -ldflags="-s -w -extldflags=-static" -o build/linux_arm64/pvr
	CGO_ENABLED=0 GOOS=windows GOARCH=386 go build -ldflags="-s -w -extldflags=-static" -o build/windows_386/pvr.exe
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -extldflags=-static" -o build/windows_amd64/pvr.exe
	CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w -extldflags=-static" -o build/darwin_amd64/pvr
	CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w -extldflags=-static" -o build/darwin_arm64/pvr

optimize:
	upx build/linux_amd64/pvr
	upx build/linux_arm/pvr
	upx build/linux_arm64/pvr
	upx build/windows_386/pvr.exe
	upx build/windows_amd64/pvr.exe
	# upx build/darwin_amd64/pvr
	# upx build/darwin_arm64/pvr

clean:
	@echo "Cleaning up..."
	rm -rf build