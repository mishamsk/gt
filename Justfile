# gt justfile - commands for development

# Default recipe: run all tests
default: test

# Run all tests
test:
    go test -v ./...

# Run unit tests only (pure functions, no git repos)
test-unit:
    go test -v -run 'TestValidate|TestTruncate|TestFormat|TestFilter|TestGetShell|TestParseArgs|TestConfigDefault' ./...

# Run integration tests only (requires real git repos)
test-integration:
    go test -v -run 'TestGetAhead|TestListWorktrees|TestRef|TestLocalBranch|TestGetDefault|TestCanFast|TestLoad|TestSave|TestEnsure|TestConfig' ./...

# Run e2e tests only (builds and runs the binary)
test-e2e:
    go test -v -run 'TestGT' ./...

# Run tests with race detection
test-race:
    go test -race -v ./...

# Generate coverage report
test-coverage:
    go test -v -coverprofile=coverage.out ./...
    go tool cover -html=coverage.out -o coverage.html
    @echo "Coverage report generated: coverage.html"

# Show coverage percentage
coverage:
    go test -cover ./...

# Build the binary
build:
    go build -o gt .

# Build with version info
build-release:
    go build -ldflags="-s -w" -o gt .

# Run linter
lint:
    golangci-lint run

# Format code
fmt:
    go fmt ./...

# Vet code
vet:
    go vet ./...

# Clean build artifacts
clean:
    rm -f gt gt-* *.zip coverage.out coverage.html
    rm -rf dist/

# Run all checks (format, vet, lint, test)
check: fmt vet test

# Install binary to GOPATH/bin
install:
    go install .

# Build for Linux (amd64 and arm64)
build-linux:
    @echo "Building for Linux (amd64)..."
    GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o gt-linux-amd64 .
    @echo "Building for Linux (arm64)..."
    GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o gt-linux-arm64 .

# Build for macOS (amd64 and arm64)
build-darwin:
    GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o gt-darwin-amd64 .
    GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o gt-darwin-arm64 .

# Build for macOS (universal binary)
build-macos:
    @echo "Building for macOS (amd64)..."
    GOOS=darwin GOARCH=amd64 go build -ldflags="-s -w" -o gt-darwin-amd64 .
    @echo "Building for macOS (arm64)..."
    GOOS=darwin GOARCH=arm64 go build -ldflags="-s -w" -o gt-darwin-arm64 .
    @echo "Creating universal binary..."
    lipo -create -output gt gt-darwin-amd64 gt-darwin-arm64
    rm gt-darwin-amd64 gt-darwin-arm64
    @echo "Universal binary created: gt"

# Code sign the macOS binary
sign: build-macos
    @echo "Code signing binary..."
    codesign --force --options runtime --sign "Developer ID Application: Ameba Labs, LLC (X93LWC49WV)" --timestamp gt
    @echo "Verifying signature..."
    codesign -dv --verbose=4 gt

# Create zip archive for notarization
package: sign
    @echo "Creating zip archive..."
    zip -r gt.zip gt
    @echo "Archive created at gt.zip"

# Submit for notarization
notarize: package
    @echo "Submitting for notarization..."
    xcrun notarytool submit gt.zip \
        --keychain-profile "notarytool-kefir" \
        --wait

# Verify notarization
verify-notarization: notarize
    @echo "Verifying notarization..."
    @echo "Note: Standalone binaries cannot be stapled, but they are still notarized"
    @echo "Extracting binary from zip..."
    unzip -o gt.zip
    @echo "Checking notarization status..."
    spctl -a -vvv -t install gt 2>&1 || true
    @echo "Binary is ready for distribution!"

# Create distribution archives for macOS
dist-macos: verify-notarization
    @echo "Creating macOS distribution archives..."
    mkdir -p dist
    cp gt dist/gt-macos-universal
    cd dist && zip -r gt-macos-universal.zip gt-macos-universal
    cd dist && shasum -a 256 gt-macos-universal.zip > gt-macos-universal.zip.sha256
    rm dist/gt-macos-universal
    @echo "macOS distribution ready in dist/"

# Build Linux distributions
dist-linux: build-linux
    @echo "Creating Linux distribution archives..."
    mkdir -p dist
    cp gt-linux-amd64 dist/
    cd dist && tar czf gt-linux-amd64.tar.gz gt-linux-amd64
    cd dist && shasum -a 256 gt-linux-amd64.tar.gz > gt-linux-amd64.tar.gz.sha256
    rm dist/gt-linux-amd64
    cp gt-linux-arm64 dist/
    cd dist && tar czf gt-linux-arm64.tar.gz gt-linux-arm64
    cd dist && shasum -a 256 gt-linux-arm64.tar.gz > gt-linux-arm64.tar.gz.sha256
    rm dist/gt-linux-arm64
    @echo "Linux distributions ready in dist/"

# Full release flow for all platforms
release: dist-macos dist-linux
    @echo "Release build complete!"
    @echo "Distribution files ready in dist/"
    @ls -lh dist/
