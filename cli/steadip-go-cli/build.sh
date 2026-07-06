mkdir -p dist

# Linux amd64
GOOS=linux GOARCH=amd64 go build -o dist/steadip-linux-amd64 .

# Linux arm64
GOOS=linux GOARCH=arm64 go build -o dist/steadip-linux-arm64 .

# macOS Intel
GOOS=darwin GOARCH=amd64 go build -o dist/steadip-darwin-amd64 .

# macOS Apple Silicon
GOOS=darwin GOARCH=arm64 go build -o dist/steadip-darwin-arm64 .

# Windows amd64
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o dist/steadip-windows-amd64.exe .

# Android
CGO_ENABLED=0 GOOS=android GOARCH=arm64 go build -o dist/steadip-android-arm64 .