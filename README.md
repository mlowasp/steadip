# SteadIP

**Free HTTP/HTTPS tunnels powered by frp.**

SteadIP lets you expose local web apps, APIs, dashboards, webhook endpoints, and homelab services behind NAT or CGNAT using a public HTTPS URL.

It is designed as a simple, developer-friendly alternative to localhost tunnel tools, with a generous free tier and low-cost verified features.

```text
https://quiet-hermit-4821.steadip.com
  -> http://localhost:3000
```

![steadip CLI/TUI](https://steadip.com/assets/images/tui.png)

---

## What SteadIP does

SteadIP creates reverse tunnels from your machine to the SteadIP gateway.

You configure tunnels in the SteadIP web dashboard, then run the local CLI:

```bash
steadip login
steadip up
```

The CLI fetches your tunnel configuration from your account, writes the local `frpc.toml`, and starts `frpc`.

SteadIP is built on top of [frp](https://github.com/fatedier/frp), a fast reverse proxy for exposing local services through a public server.

---

## Features

### Free accounts

Free accounts are intended for HTTP/HTTPS tunnels.

- Free HTTP/HTTPS tunnels
- Automatic HTTPS
- Random SteadIP subdomain
- Works behind NAT and CGNAT
- Dashboard-managed tunnel configuration
- CLI client for Linux, macOS, and Windows

Example free URL:

```text
https://quiet-hermit-4821.steadip.com
```

### Verified accounts

Verified accounts unlock trusted features.

- Custom SteadIP subdomain
- Bring your own custom domain
- SSH/TCP tunnels
- Higher limits
- More active tunnels

Verified accounts are **$5 USD/year** and help protect the free tunnel network from abuse.

---

## How it works

```text
Browser / Webhook / Client
        |
        v
https://your-name.steadip.com
        |
        v
SteadIP gateway
        |
        v
frp reverse tunnel
        |
        v
your local service
```

For HTTP/HTTPS tunnels, many users share the same public ports. Routing is done by hostname.

For TCP/SSH tunnels, verified users receive a unique public high port.

---

## Install

### Linux

```bash
curl -fsSL https://steadip.com/install-linux.sh | sh
```

### macOS

```bash
curl -fsSL https://steadip.com/install-macos.sh | sh
```

### Windows PowerShell

```powershell
irm https://steadip.com/install-windows.ps1 | iex
```

After installation, open a new terminal if the `steadip` command is not immediately available.

---

## Quick start

### 1. Create an account

Create a free account at:

```text
https://steadip.com
```

### 2. Create a tunnel in the dashboard

In the SteadIP dashboard, create a tunnel such as:

```text
Type: HTTP
Local host: 127.0.0.1
Local port: 3000
```

Free accounts receive a random SteadIP subdomain.

Example:

```text
quiet-hermit-4821.steadip.com
```

### 3. Log in from the CLI

```bash
steadip login
```

The CLI will show a device code and open the browser.

Approve the login in the SteadIP webapp.

### 4. Start your tunnel

```bash
steadip up
```

Your local service is now reachable through the public SteadIP URL.

---

## CLI commands

### `steadip login`

Starts the browser/device-code login flow.

```bash
steadip login
```

Use this the first time you install the SteadIP client.

---

### `steadip monitor`

Starts the htop-like tunnel monitor.

```bash
steadip monitor
```

![Starts the htop-like tunnel monitor.](https://steadip.com/assets/images/monitor.png)

---

### `steadip relogin`

Log in again using a device code generated from the webapp.

```bash
steadip relogin
```

This is useful when you already authorized a tunnel in the dashboard and want this machine to use that tunnel configuration.

Example flow:

```bash
steadip down
steadip relogin
steadip up
```

The command prompts for a device code from the SteadIP webapp, exchanges it for a local CLI token, clears the old local tunnel config, and lets you start the newly authorized tunnel with `steadip up`.

---

### `steadip sync`

Fetches the latest tunnel configuration from the dashboard and writes the local `frpc.toml`.

```bash
steadip sync
```

This does not start the tunnel.

---

### `steadip up`

Fetches the latest tunnel configuration and starts the tunnel.

```bash
steadip up
```

Use this after creating or editing tunnels in the dashboard.

---

### `steadip down`

Stops the running tunnel.

```bash
steadip down
```

This does not delete your tunnel from the dashboard.

---

### `steadip enable`

Enables auto-start.

```bash
steadip enable
```

Platform behavior:

- Linux: installs and starts a systemd user service
- macOS: installs and starts a LaunchAgent
- Windows: installs and starts a Scheduled Task

---

### `steadip disable`

Disables auto-start.

```bash
steadip disable
```

This stops and removes the local auto-start configuration.

---

### `steadip status`

Shows the local tunnel status.

```bash
steadip status
```

---

### `steadip logs`

Shows the local `frpc` logs.

```bash
steadip logs
```

---

### `steadip config`

Prints the local `frpc.toml` with secrets hidden.

```bash
steadip config
```

---

### `steadip logout`

Stops the local tunnel and removes the saved CLI token.

```bash
steadip logout
```

This does not delete your account or dashboard tunnels.

---

### `steadip uninstall`

Removes local SteadIP client files.

```bash
steadip uninstall
```

---

## Dashboard-managed configuration

SteadIP keeps tunnel configuration in the webapp.

The CLI is intentionally simple:

```bash
steadip login
steadip up
steadip down
```

You configure these in the dashboard:

- Tunnel type
- Local host
- Local port
- Public hostname
- Custom domain
- TCP/SSH port
- Tunnel status
- Token rotation
- Deletion

Then the CLI fetches the generated frp config.

This keeps the CLI simple and makes Windows/macOS support easier.

---

## HTTP/HTTPS tunnels

HTTP/HTTPS tunnels are the default SteadIP tunnel type.

Example:

```text
https://quiet-hermit-4821.steadip.com
  -> http://127.0.0.1:3000
```

Good use cases:

- Local development servers
- Webhook testing
- Home Assistant
- Uptime Kuma
- Grafana
- Nextcloud
- Jellyfin or Plex web UI
- WordPress staging sites
- Laravel/Rails/Node.js apps
- Internal dashboards

Free users get random subdomains.

Verified users can use custom SteadIP subdomains or custom domains.

---

## Custom domains

Custom domains are available for verified users.

Example:

```text
https://app.example.com
  -> http://127.0.0.1:3000
```

The dashboard will show the DNS record you need to create.

Usually this is a `CNAME` pointing to the tunnel's assigned steadip gateway, ie;

```
app.example.com CNAME il.gw.steadip.com
```

---

## SSH/TCP tunnels

SSH/TCP tunnels are available for verified users.

Example public endpoint:

```text
gateway.steadip.com:43122
```

Example local destination:

```text
127.0.0.1:22
```

Connect with:

```bash
ssh -p 43122 user@gateway.steadip.com
```

TCP tunnels use assigned high ports. Users do not choose arbitrary public ports.

---

## Bandwidth and limits

SteadIP tracks bandwidth usage per tunnel.

HTTP/HTTPS bandwidth is measured from gateway logs using the hostname associated with each tunnel.

The dashboard shows your usage.

Typical limits:

| Plan | Monthly bandwidth | Daily fair-use limit |
| --- | ---: | ---: |
| Free | 50 GB/month | 5 GB/day |
| Verified | 500 GB/month | 50 GB/day |

Limits may be adjusted over time to protect the network from abuse.

If a tunnel exceeds its quota, SteadIP may temporarily suspend the tunnel until the next cycle.

---

## Auto-start

### Linux

`steadip enable` creates a systemd user service:

```text
~/.config/systemd/user/steadip.service
```

Useful commands:

```bash
systemctl --user status steadip.service
systemctl --user restart steadip.service
journalctl --user -u steadip.service -f
```

To allow the service to start before login:

```bash
loginctl enable-linger "$USER"
```

### macOS

`steadip enable` creates a LaunchAgent:

```text
~/Library/LaunchAgents/com.steadip.client.plist
```

### Windows

`steadip enable` creates a Windows Scheduled Task:

```text
SteadIP Tunnel Client
```

---

## Local files

### Linux

```text
~/.local/bin/steadip/
~/.config/steadip/
~/.local/state/steadip/
```

### macOS

```text
~/.local/bin/steadip/
~/.config/steadip/
~/.local/state/steadip/
~/Library/LaunchAgents/com.steadip.client.plist
```

### Windows

```text
%LOCALAPPDATA%\SteadIP\
%APPDATA%\SteadIP\
```

---

## Authentication model

SteadIP uses browser/device-code authentication.

The CLI stores a local access token after login.

Tunnel connections use generated frp connection credentials returned by the SteadIP API.

Users should not manually edit `frpc.toml`; the server-side auth plugin validates tunnel ownership, hostname ownership, port assignment, and account status.

---

## Security model

SteadIP does not trust user-supplied tunnel config.

Even if a user edits the local `frpc.toml`, SteadIP validates:

- The tunnel belongs to the authenticated user
- The connection token belongs to the tunnel
- The requested subdomain belongs to the tunnel
- The requested custom domain belongs to the tunnel
- The requested TCP port belongs to the tunnel
- The user is allowed to use TCP/SSH
- The tunnel is active and not suspended
- The account is not over quota

This prevents users from taking over another user’s subdomain or port.

---

## Abuse policy

SteadIP is for legitimate development, homelab, webhook, dashboard, and remote-access use cases.

The following are not allowed:

- Phishing
- Malware
- Credential harvesting
- Spam
- Open proxies
- Botnets
- Abusive scraping
- Illegal content
- Public file distribution abuse
- Attempts to bypass quotas or access controls

SteadIP may suspend tunnels or accounts that violate these rules.

---

## Troubleshooting

### `steadip: command not found`

Open a new terminal after installation.

On Linux/macOS, make sure this directory is in your `PATH`:

```bash
~/.local/bin/steadip
```

The installer also tries to create:

```bash
/usr/local/bin/steadip
```

### Login keeps waiting

The webapp may not have approved the device code yet.

Run:

```bash
steadip login
```

Then approve the login in the browser.

If you hit the tunnel limit, delete an old tunnel in the dashboard and try again.

### Tunnel starts, but URL does not work

Check your local service first:

```bash
curl http://127.0.0.1:3000
```

Then check logs:

```bash
steadip logs
```

Make sure the dashboard tunnel local host and local port are correct.

### I changed the tunnel in the dashboard

Run:

```bash
steadip up
```

or:

```bash
steadip sync
steadip down
steadip up
```

### Auto-start is enabled but tunnel is not running

Check status:

```bash
steadip status
```

Linux logs:

```bash
journalctl --user -u steadip.service -f
```

macOS logs are written under:

```text
~/.local/state/steadip/
```

Windows logs are written under:

```text
%LOCALAPPDATA%\SteadIP\state\
```

---

## Development notes

SteadIP currently uses:

- frp / frpc / frps
- nginx for HTTP/HTTPS gatewaying and bandwidth logs
- SteadIP API for tunnel configuration
- server-side frp auth plugin for enforcement
- Cloudflare DNS for SteadIP subdomains and custom-domain routing

---

## API shape expected by the CLI

The CLI fetches tunnel config with:

```http
GET /api/device/config
Authorization: Bearer sat_xxx
```

The response should include a generated frp config string:

```json
{
  "frp": "serverAddr = \"gateway.steadip.com\"\nserverPort = 7000\n..."
}
```

The CLI writes that string directly to:

```text
~/.config/steadip/frpc.toml
```

or the equivalent platform path.

---

## Links

- Website: <https://steadip.com>
- Dashboard: <https://steadip.com>
- frp project: <https://github.com/fatedier/frp>
