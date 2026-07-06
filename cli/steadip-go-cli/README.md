# SteadIP Go CLI

A slick Bubble Tea + Bubbles + Lip Gloss terminal UI for SteadIP.

## Commands

```bash
steadip                 # interactive TUI
steadip login
steadip relogin
steadip sync
steadip up
steadip down
steadip enable
steadip disable
steadip status
steadip logs
steadip config
steadip logout
```

## Build

```bash
go mod tidy
go build -o steadip .
```

## Cross-compile examples

```bash
GOOS=linux GOARCH=amd64 go build -o dist/steadip-linux-amd64 .
GOOS=darwin GOARCH=arm64 go build -o dist/steadip-darwin-arm64 .
GOOS=windows GOARCH=amd64 go build -o dist/steadip-windows-amd64.exe .
```

## API assumptions

The client expects your existing SteadIP API:

```text
POST /api/device/code
POST /api/device/token
GET  /api/device/config
```

`GET /api/device/config` should return:

```json
{
  "frp": "serverAddr = \"gw1.steadip.com\"\nserverPort = 7000\n..."
}
```

The CLI writes the returned `frp` string directly to the local `frpc.toml`.
