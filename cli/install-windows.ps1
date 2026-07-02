# SteadIP Windows installer
# Run with:
#   powershell -ExecutionPolicy Bypass -File .\install.ps1

param(
    [string]$InstallDir = "$env:LOCALAPPDATA\SteadIP",
    [string]$FrpVersion = "0.61.1"
)

$ErrorActionPreference = "Stop"

$BinDir = Join-Path $InstallDir "bin"
$ConfigDir = Join-Path $env:APPDATA "SteadIP"
$StateDir = Join-Path $env:LOCALAPPDATA "SteadIP\state"

$SteadipApi = "https://steadip.com/api"
$SteadipDashboard = "https://steadip.com"

function Get-SteadipArch {
    switch ($env:PROCESSOR_ARCHITECTURE) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "Unsupported Windows architecture: $env:PROCESSOR_ARCHITECTURE" }
    }
}

function Install-Frpc {
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null

    $arch = Get-SteadipArch
    $archive = "frp_$($FrpVersion)_windows_$($arch).zip"
    $url = "https://github.com/fatedier/frp/releases/download/v$FrpVersion/$archive"
    $tmp = Join-Path $env:TEMP ([System.Guid]::NewGuid().ToString())
    New-Item -ItemType Directory -Force -Path $tmp | Out-Null

    try {
        $zip = Join-Path $tmp $archive
        Write-Host "Downloading frpc v$FrpVersion for windows/$arch..."
        Invoke-WebRequest -Uri $url -OutFile $zip

        Expand-Archive -Force -Path $zip -DestinationPath $tmp
        $frpc = Get-ChildItem -Path $tmp -Recurse -Filter "frpc.exe" | Select-Object -First 1

        if (-not $frpc) {
            throw "Could not find frpc.exe in downloaded archive."
        }

        Copy-Item -Force $frpc.FullName (Join-Path $BinDir "frpc.exe")
        Write-Host "Installed frpc to $(Join-Path $BinDir 'frpc.exe')"
    }
    finally {
        Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
    }
}

function Install-SteadipCli {
    New-Item -ItemType Directory -Force -Path $BinDir, $ConfigDir, $StateDir | Out-Null

    $cliPath = Join-Path $BinDir "steadip.ps1"
    $cmdPath = Join-Path $BinDir "steadip.cmd"

    $cli = @'
param(
    [Parameter(Position=0)]
    [string]$Command,

    [Parameter(ValueFromRemainingArguments=$true)]
    [string[]]$RemainingArgs
)

$ErrorActionPreference = "Stop"

$ConfigDir = if ($env:STEADIP_CONFIG_DIR) { $env:STEADIP_CONFIG_DIR } else { Join-Path $env:APPDATA "SteadIP" }
$StateDir = if ($env:STEADIP_STATE_DIR) { $env:STEADIP_STATE_DIR } else { Join-Path $env:LOCALAPPDATA "SteadIP\state" }
$BinDir = if ($env:STEADIP_BIN_DIR) { $env:STEADIP_BIN_DIR } else { Join-Path $env:LOCALAPPDATA "SteadIP\bin" }

$Frpc = Join-Path $BinDir "frpc.exe"
$TokenFile = Join-Path $ConfigDir "token"
$ConfigFile = Join-Path $ConfigDir "frpc.toml"
$MetaFile = Join-Path $ConfigDir "tunnels.json"
$PidFile = Join-Path $StateDir "frpc.pid"
$LogFile = Join-Path $StateDir "frpc.log"
$TaskName = "SteadIP Tunnel Client"

$SteadipApi = "https://steadip.com/api"
$SteadipDashboard = "https://steadip.com"

New-Item -ItemType Directory -Force -Path $ConfigDir, $StateDir | Out-Null

function Show-Usage {
@"
SteadIP CLI for Windows

Usage:
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
  steadip uninstall

Commands:
  login      Sign in using browser/device-code login
  relogin    Sign in using a device code generated in the webapp
  sync       Fetch dashboard tunnel config and write frpc.toml
  up         Sync and start tunnels now
  down       Stop running tunnels now, without changing auto-start setting
  enable     Enable and start Windows Scheduled Task auto-start daemon
  disable    Stop and disable Windows Scheduled Task auto-start daemon
  status     Show tunnel and daemon status
  logs       Show frpc logs
  config     Print local frpc config with secrets hidden
  logout     Stop tunnels and remove local login token
  uninstall  Remove SteadIP client files

Dashboard:
  Configure tunnels at https://steadip.com
"@
}

function Invoke-SteadipPostJson {
    param(
        [string]$Url,
        [object]$Body,
        [string]$Token = ""
    )

    $headers = @{ "Content-Type" = "application/json" }
    if ($Token) { $headers["Authorization"] = "Bearer $Token" }

    $json = if ($Body -is [string]) { $Body } else { $Body | ConvertTo-Json -Depth 20 -Compress }
    return Invoke-RestMethod -Method Post -Uri $Url -Headers $headers -Body $json
}

function Invoke-SteadipPostWithStatus {
    param(
        [string]$Url,
        [object]$Body,
        [string]$Token = ""
    )

    $headers = @{ "Content-Type" = "application/json"; "Accept" = "application/json" }
    if ($Token) { $headers["Authorization"] = "Bearer $Token" }

    $json = if ($Body -is [string]) { $Body } else { $Body | ConvertTo-Json -Depth 20 -Compress }

    try {
        $response = Invoke-WebRequest -Method Post -Uri $Url -Headers $headers -Body $json -UseBasicParsing
        $body = $null
        if ($response.Content) {
            try { $body = $response.Content | ConvertFrom-Json } catch { $body = $null }
        }
        return [pscustomobject]@{ StatusCode = [int]$response.StatusCode; Body = $body; RawBody = $response.Content }
    }
    catch {
        $statusCode = 0
        $rawBody = ""
        if ($_.Exception.Response) {
            $statusCode = [int]$_.Exception.Response.StatusCode.value__
            try {
                $stream = $_.Exception.Response.GetResponseStream()
                $reader = New-Object System.IO.StreamReader($stream)
                $rawBody = $reader.ReadToEnd()
                $reader.Close()
            } catch { $rawBody = "" }
        }
        $body = $null
        if ($rawBody) {
            try { $body = $rawBody | ConvertFrom-Json } catch { $body = $null }
        }
        return [pscustomobject]@{ StatusCode = $statusCode; Body = $body; RawBody = $rawBody }
    }
}

function Invoke-SteadipGetJson {
    param(
        [string]$Url,
        [string]$Token = ""
    )

    $headers = @{ "Accept" = "application/json" }
    if ($Token) { $headers["Authorization"] = "Bearer $Token" }

    return Invoke-RestMethod -Method Get -Uri $Url -Headers $headers
}

function Get-Token {
    if (Test-Path $TokenFile) {
        return (Get-Content -Raw $TokenFile).Trim()
    }
    return ""
}

function Save-Token {
    param([string]$Token)
    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    Set-Content -NoNewline -Encoding ASCII -Path $TokenFile -Value $Token
}

function Require-Login {
    $token = Get-Token
    if (-not $token) {
        Write-Error "You are not logged in. Run: steadip login"
    }
    return $token
}

function Get-ManualProcess {
    if (-not (Test-Path $PidFile)) { return $null }
    $pidText = (Get-Content -Raw $PidFile).Trim()
    if (-not $pidText) { return $null }
    try {
        return Get-Process -Id ([int]$pidText) -ErrorAction Stop
    }
    catch {
        return $null
    }
}

function Test-TaskExists {
    return [bool](Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue)
}

function Test-TaskRunning {
    $task = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    return ($task -and $task.State -eq "Running")
}

function Write-FrpcConfigFromApiResponse {
    param([object]$Response)

    $frp = [string]$Response.frp
    Set-Content -Encoding ASCII -Path $ConfigFile -Value $frp
}

function Cmd-Login {
    $device = $env:COMPUTERNAME
    $payload = @{
        client_name = "steadip-cli"
        client_version = "0.2.0"
        device_name = $device
    }

    Write-Host "Requesting SteadIP device login..."
    $response = Invoke-SteadipPostJson "$SteadipApi/device/code" $payload

    $deviceCode = $response.device_code
    $userCode = $response.user_code
    $verificationUri = $response.verification_uri
    $verificationUriComplete = $response.verification_uri_complete
    $interval = if ($response.interval) { [int]$response.interval } else { 5 }
    $expiresIn = if ($response.expires_in) { [int]$response.expires_in } else { 600 }

    Write-Host ""
    Write-Host "SteadIP CLI login"
    Write-Host ""
    Write-Host "Open this page:"
    Write-Host "  $verificationUri"
    Write-Host ""
    Write-Host "Enter code:"
    Write-Host "  $deviceCode"
    Write-Host ""

    if ($verificationUriComplete) {
        Start-Process $verificationUriComplete | Out-Null
    }

    Write-Host "Waiting for authorization..."
    $started = Get-Date

    while ($true) {
        if (((Get-Date) - $started).TotalSeconds -ge $expiresIn) {
            Write-Error "Login expired. Run 'steadip login' again."
        }

        Start-Sleep -Seconds $interval

        try {
            $tokenResponse = Invoke-SteadipPostJson "$SteadipApi/device/token" @{
                device_code = $deviceCode
                user_code = $userCode
            }
        }
        catch {
            $errorCode = ""
            try {
                $stream = $_.Exception.Response.GetResponseStream()
                if ($stream) {
                    $reader = New-Object System.IO.StreamReader($stream)
                    $body = $reader.ReadToEnd()
                    $parsed = $body | ConvertFrom-Json
                    $errorCode = $parsed.error
                }
            } catch {}

            if (-not $errorCode -or $errorCode -eq "authorization_pending") {
                Write-Host -NoNewline "."
                continue
            }
            if ($errorCode -eq "slow_down") {
                $interval += 5
                Write-Host -NoNewline "."
                continue
            }
            if ($errorCode -eq "tunnels_limit_reached") { Write-Error "Maximum number of tunnels reached." }
            if ($errorCode -eq "expired_token") { Write-Error "Login expired." }
            if ($errorCode -eq "access_denied") { Write-Error "Login was denied." }
            if ($errorCode -eq "no_device_code") { Write-Error "Device code was lost in transport." }

            Write-Host -NoNewline "."
            continue
        }

        Save-Token $tokenResponse.access_token

        Write-Host ""
        Write-Host ""
        Write-Host "Logged in successfully."
        if ($tokenResponse.user_email) { Write-Host "Account: $($tokenResponse.user_email)" }
        if ($tokenResponse.user_verified -eq $true -or $tokenResponse.user_verified -eq "true") { Write-Host "Plan: Verified" } else { Write-Host "Plan: Free" }
        Write-Host ""
        Write-Host "Configure tunnels in your dashboard:"
        Write-Host "  $SteadipDashboard"
        Write-Host ""
        Write-Host "Then run:"
        Write-Host "  steadip up"
        Write-Host ""
        return
    }
}


function Cmd-Relogin {
    $deviceCode = Read-Host "Enter device code from SteadIP webapp"
    $deviceCode = ($deviceCode -replace '\s', '')

    if (-not $deviceCode) {
        Write-Error "Missing device code."
    }

    Write-Host "Authorizing this device with SteadIP..."

    $poll = Invoke-SteadipPostWithStatus "$SteadipApi/device/token" @{
        device_code = $deviceCode
        relogin = $true
        client_name = "steadip-cli"
        client_version = "0.2.2"
        device_name = $env:COMPUTERNAME
    }

    if ($poll.StatusCode -ne 200) {
        $errorCode = ""
        if ($poll.Body -and $poll.Body.error) { $errorCode = [string]$poll.Body.error }

        if ($errorCode -eq "tunnels_limit_reached") { Write-Error "Maximum number of tunnels reached. Delete an existing tunnel from your SteadIP dashboard, then try again." }
        if ($errorCode -eq "expired_token") { Write-Error "Device code expired. Generate a new one from the SteadIP dashboard." }
        if ($errorCode -eq "access_denied") { Write-Error "Device code was denied." }
        if ($errorCode -eq "invalid_device_code") { Write-Error "Invalid device code." }
        if ($errorCode -eq "no_device_code") { Write-Error "Missing device code." }

        if (-not $errorCode) { $errorCode = "HTTP $($poll.StatusCode)" }
        Write-Error "Relogin failed: $errorCode"
    }

    $body = $poll.Body
    Save-Token $body.access_token
    Remove-Item -Force $ConfigFile, $MetaFile -ErrorAction SilentlyContinue

    Write-Host ""
    Write-Host "Relogin successful."
    if ($body.user_email) { Write-Host "Account: $($body.user_email)" }
    if ($body.user_verified -eq $true -or $body.user_verified -eq "true") { Write-Host "Plan: Verified" } else { Write-Host "Plan: Free" }
    Write-Host ""
    Write-Host "Run: steadip up"
}

function Cmd-Sync {
    $token = Require-Login
    Write-Host "Fetching SteadIP tunnel config..."
    $response = Invoke-SteadipGetJson "$SteadipApi/device/config" $token

    $response | ConvertTo-Json -Depth 50 | Set-Content -Encoding UTF8 -Path $MetaFile
    Write-FrpcConfigFromApiResponse $response

    Write-Host "Config written:"
    Write-Host "  $ConfigFile"
}

function Start-ManualFrpc {
    if (-not (Test-Path $Frpc)) { Write-Error "frpc is missing: $Frpc" }
    if (-not (Test-Path $ConfigFile)) { Write-Error "No frpc config found. Run: steadip sync" }

    $old = Get-ManualProcess
    if ($old) { Stop-Process -Id $old.Id -Force -ErrorAction SilentlyContinue }

    Write-Host ""
    Write-Host "Starting SteadIP tunnels..."

    $out = New-Object System.Diagnostics.ProcessStartInfo
    $out.FileName = $Frpc
    $out.Arguments = "-c `"$ConfigFile`""
    $out.RedirectStandardOutput = $true
    $out.RedirectStandardError = $true
    $out.UseShellExecute = $false
    $out.CreateNoWindow = $true

    $process = New-Object System.Diagnostics.Process
    $process.StartInfo = $out
    $process.Start() | Out-Null
    Set-Content -NoNewline -Path $PidFile -Value $process.Id

    Start-Sleep -Seconds 1

    if (Get-ManualProcess) {
        Write-Host "Started."
        Write-Host "Logs: $LogFile"
    }
    else {
        Write-Error "frpc failed to start."
    }
}

function Cmd-Up {
    Cmd-Sync
    if (Test-TaskRunning) {
        Write-Host ""
        Write-Host "SteadIP Scheduled Task is running. Restarting with latest config..."
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Start-ScheduledTask -TaskName $TaskName
        Write-Host "Restarted."
        return
    }
    Start-ManualFrpc
}

function Cmd-Down {
    $stopped = $false
    $proc = Get-ManualProcess
    if ($proc) {
        Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        Remove-Item -Force $PidFile -ErrorAction SilentlyContinue
        Write-Host "Stopped manually started SteadIP tunnel."
        $stopped = $true
    }

    if (Test-TaskRunning) {
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Write-Host "Stopped SteadIP Scheduled Task daemon."
        $stopped = $true
    }

    if (-not $stopped) { Write-Host "SteadIP tunnel is not running." }
}

function Cmd-Daemon {
    $token = Require-Login
    Write-Host "Fetching SteadIP tunnel config..."
    $response = Invoke-SteadipGetJson "$SteadipApi/device/config" $token
    $response | ConvertTo-Json -Depth 50 | Set-Content -Encoding UTF8 -Path $MetaFile
    Write-FrpcConfigFromApiResponse $response

    Write-Host "Starting frpc in daemon mode..."

    & $Frpc -c $ConfigFile
}

function Cmd-Enable {
    Require-Login | Out-Null

    $ps = (Get-Process -Id $PID).Path
    $action = New-ScheduledTaskAction -Execute $ps -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$PSCommandPath`" daemon"
    $trigger = New-ScheduledTaskTrigger -AtLogOn -User $env:USERNAME
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1)

    if (Test-TaskExists) {
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    }

    Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings -Description "SteadIP Tunnel Client" | Out-Null
    Start-ScheduledTask -TaskName $TaskName

    Write-Host "SteadIP auto-start enabled and started."
    Write-Host "Task: $TaskName"
}

function Cmd-Disable {
    if (Test-TaskExists) {
        Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
        Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    }
    Write-Host "SteadIP auto-start disabled."
}

function Cmd-Status {
    $proc = Get-ManualProcess
    if ($proc) {
        Write-Host "Manual tunnel: running"
        Write-Host "Manual PID: $($proc.Id)"
    }
    else {
        Write-Host "Manual tunnel: stopped"
    }

    if (Test-TaskExists) { Write-Host "Auto-start: enabled" } else { Write-Host "Auto-start: disabled" }
    if (Test-TaskRunning) { Write-Host "Daemon: running" } else { Write-Host "Daemon: stopped" }

    if (Test-Path $ConfigFile) { Write-Host "Config: $ConfigFile" }


    if (Test-Path $LogFile) { Write-Host "Manual logs: $LogFile" }
}

function Cmd-Logs {
    if (-not (Test-Path $LogFile)) {
        Write-Host "No manual logs yet."
        Write-Host "For the Scheduled Task, check Windows Task Scheduler history or Event Viewer."
        return
    }
    Get-Content -Path $LogFile -Wait -Tail 120
}

function Cmd-Config {
    if (-not (Test-Path $ConfigFile)) {
        Write-Host "No config found."
        return
    }
    Get-Content -Raw $ConfigFile | ForEach-Object {
        $_ -replace 'connection_token\s*=\s*".*"', 'connection_token = "***"'
    }
}

function Cmd-Logout {
    Cmd-Down | Out-Null
    Remove-Item -Force $TokenFile -ErrorAction SilentlyContinue
    Write-Host "Logged out."
}

function Cmd-Uninstall {
    Cmd-Down | Out-Null
    Cmd-Disable | Out-Null

    Write-Host "This will remove:"
    Write-Host "  $BinDir"
    Write-Host "  $ConfigDir"
    Write-Host "  $StateDir"
    $answer = Read-Host "Continue? [y/N]"
    if ($answer -in @("y", "Y", "yes", "YES")) {
        Remove-Item -Recurse -Force $BinDir, $ConfigDir, $StateDir -ErrorAction SilentlyContinue
        Write-Host "SteadIP removed."
    }
    else {
        Write-Host "Cancelled."
    }
}

switch ($Command) {
    "login" { Cmd-Login }
    "relogin" { Cmd-Relogin }
    "sync" { Cmd-Sync }
    "up" { Cmd-Up }
    "down" { Cmd-Down }
    "enable" { Cmd-Enable }
    "disable" { Cmd-Disable }
    "status" { Cmd-Status }
    "logs" { Cmd-Logs }
    "config" { Cmd-Config }
    "logout" { Cmd-Logout }
    "uninstall" { Cmd-Uninstall }
    "daemon" { Cmd-Daemon }
    { $_ -in @($null, "", "help", "-h", "--help") } { Show-Usage }
    default {
        Write-Error "Unknown command: $Command`n`n$(Show-Usage)"
    }
}
'@

    Set-Content -Encoding UTF8 -Path $cliPath -Value $cli

    $cmd = "@echo off`r`npowershell -NoProfile -ExecutionPolicy Bypass -File `"$cliPath`" %*`r`n"
    Set-Content -Encoding ASCII -Path $cmdPath -Value $cmd

    Write-Host "Installed SteadIP CLI to $cliPath"
    Write-Host "Installed command shim to $cmdPath"
}

function Add-BinToUserPath {
    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    if (-not $current) { $current = "" }

    $parts = $current -split ";" | Where-Object { $_ }
    if ($parts -notcontains $BinDir) {
        $newPath = if ($current) { "$current;$BinDir" } else { $BinDir }
        [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
        Write-Host "Added $BinDir to your user PATH."
        Write-Host "Open a new terminal before running: steadip login"
    }
    else {
        Write-Host "$BinDir is already in your user PATH."
    }
}

New-Item -ItemType Directory -Force -Path $BinDir, $ConfigDir, $StateDir | Out-Null
Install-Frpc
Install-SteadipCli
Add-BinToUserPath

Write-Host ""
Write-Host "SteadIP installed for Windows."
Write-Host ""
Write-Host "Next steps:"
Write-Host "  steadip login"
Write-Host "  steadip up"
Write-Host ""
Write-Host "Auto-start:"
Write-Host "  steadip enable"
Write-Host ""
Write-Host "Dashboard:"
Write-Host "  https://steadip.com"
