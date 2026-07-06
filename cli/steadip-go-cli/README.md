# SteadIP Go CLI

Fixed build with:

- `steadip` opens the interactive TUI.
- `steadip login` runs a command-line device-code login flow.
- TUI Login displays the device code and URL correctly.
- Device-code JSON fields use proper snake_case tags.
- Relogin remains a TUI modal when launched from the TUI.
- Relogin remains a CLI prompt when using `steadip relogin`.

## Build

```bash
go mod tidy
go build -o steadip .
./build.sh
```

## QR code login

This build adds a terminal QR code on the TUI login screen and command-line `steadip login` output. The QR is generated from `verification_uri_complete` when available, falling back to `verification_uri`.

New dependency:

```bash
go get github.com/skip2/go-qrcode
```

The TUI only shows the QR code when the terminal is large enough; otherwise it still shows the URL and device code.
