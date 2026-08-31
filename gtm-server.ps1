<#
.SYNOPSIS
  Start / stop / inspect the Redpoint GTM MCP server.

.DESCRIPTION
  Runs the server as ONE compiled process instead of `go run .`, which spawns a
  `go` parent plus an opaque child under the build cache. A `go build` binary
  carries its own git commit (vcs.revision), so `status` can always answer
  "which version is running?" -- a `go run` binary carries nothing, which is how
  a stale server holding old credentials went unnoticed for an hour on
  2026-08-31.

  Guardrails:
    * start refuses if anything already listens on the port (no second server)
    * start rebuilds first and ABORTS on a compile error rather than silently
      launching a stale binary
    * status compares the running binary's commit against the working tree

.EXAMPLE
  .\gtm-server.ps1 status
  .\gtm-server.ps1 start
  .\gtm-server.ps1 restart
#>
[CmdletBinding()]
param(
    [Parameter(Position = 0)]
    [ValidateSet('start', 'stop', 'restart', 'status')]
    [string]$Action = 'status',

    [int]$Port = 8080
)

$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.Encoding]::UTF8

$Repo    = $PSScriptRoot
$Exe     = Join-Path $Repo 'gtm-mcp-server.exe'
$LogDir  = Join-Path $Repo 'logs'
$OutLog  = Join-Path $LogDir 'server.out.log'
$ErrLog  = Join-Path $LogDir 'server.err.log'
$RunLog  = Join-Path $LogDir 'lifecycle.log'

function Write-RunLog {
    param([string]$Message)
    if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir | Out-Null }
    $line = "{0}  {1}`r`n" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message
    # AppendAllText, not Add-Content: Add-Content honours an existing BOM over
    # -Encoding and can leave a UTF-16 log that nothing greps cleanly.
    [System.IO.File]::AppendAllText($RunLog, $line, [System.Text.UTF8Encoding]::new($false))
}

function Get-Listener {
    $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue
    if (-not $conn) { return $null }
    return Get-Process -Id ($conn | Select-Object -First 1).OwningProcess -ErrorAction SilentlyContinue
}

function Get-BinaryRevision {
    param([string]$Path)
    if (-not (Test-Path $Path)) { return $null }
    $info = go version -m $Path
    $rev = $info | Select-String -Pattern 'vcs\.revision=(\S+)'
    $mod = $info | Select-String -Pattern 'vcs\.modified=(\S+)'
    if (-not $rev) { return [pscustomobject]@{ Revision = '(unstamped -- built via `go run`?)'; Modified = $null } }
    return [pscustomobject]@{
        Revision = $rev.Matches[0].Groups[1].Value
        Modified = if ($mod) { $mod.Matches[0].Groups[1].Value } else { 'unknown' }
    }
}

function Invoke-Build {
    Write-Host "Building $Exe ..." -ForegroundColor Cyan
    Push-Location $Repo
    try {
        go build -o gtm-mcp-server.exe .
        if ($LASTEXITCODE -ne 0) {
            throw "go build failed (exit $LASTEXITCODE). Refusing to start a stale binary."
        }
    } finally {
        Pop-Location
    }
    $r = Get-BinaryRevision -Path $Exe
    Write-Host ("Built  {0}  (tree modified: {1})" -f $r.Revision, $r.Modified) -ForegroundColor Green
}

function Show-Status {
    $proc = Get-Listener
    Write-Host ''
    Write-Host "GTM MCP server -- port $Port" -ForegroundColor Cyan
    Write-Host ('-' * 52)

    if (-not $proc) {
        Write-Host 'State      : NOT RUNNING' -ForegroundColor Yellow
    } else {
        Write-Host 'State      : RUNNING' -ForegroundColor Green
        Write-Host ("PID        : {0}" -f $proc.Id)
        Write-Host ("Started    : {0}" -f $proc.StartTime)
        Write-Host ("Binary     : {0}" -f $proc.Path)

        if ($proc.Path -like '*\go-build\*') {
            Write-Host 'Provenance : go-build CACHE path -- this is a `go run` process.' -ForegroundColor Yellow
            Write-Host '             Its source commit is NOT recoverable. Restart via' -ForegroundColor Yellow
            Write-Host '             `.\gtm-server.ps1 restart` to get a stamped binary.' -ForegroundColor Yellow
        } else {
            $r = Get-BinaryRevision -Path $proc.Path
            Write-Host ("Revision   : {0}" -f $r.Revision)
            Write-Host ("Dirty tree : {0}" -f $r.Modified)

            $head = (git -C $Repo rev-parse HEAD).Trim()
            Write-Host ("Repo HEAD  : {0}" -f $head)
            if ($r.Revision -ne $head) {
                Write-Host 'WARNING    : running binary predates the working tree. Restart.' -ForegroundColor Red
            }
        }
    }

    # /health needs no auth, so this is a safe liveness probe.
    try {
        $h = Invoke-RestMethod -Uri "http://localhost:$Port/health" -TimeoutSec 4
        Write-Host ("Health     : {0} (v{1})" -f $h.status, $h.version)
    } catch {
        Write-Host 'Health     : unreachable' -ForegroundColor Yellow
    }

    $tokenStore = Join-Path $Repo '.gtm-token-store.json'
    if (Test-Path $tokenStore) {
        Write-Host ("Token store: present, updated {0}" -f (Get-Item $tokenStore).LastWriteTime)
    } else {
        Write-Host 'Token store: absent -- first use will need a browser OAuth round-trip.' -ForegroundColor Yellow
    }
    Write-Host ''
}

function Start-Server {
    $existing = Get-Listener
    if ($existing) {
        Write-Host ("Port $Port is already held by PID {0} ({1}). Not starting a second server." -f $existing.Id, $existing.ProcessName) -ForegroundColor Yellow
        Write-Host 'Use `.\gtm-server.ps1 restart` if you intend to replace it.' -ForegroundColor Yellow
        Show-Status
        return
    }

    Invoke-Build
    if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir | Out-Null }

    $proc = Start-Process -FilePath $Exe `
        -WorkingDirectory $Repo `
        -WindowStyle Hidden `
        -RedirectStandardOutput $OutLog `
        -RedirectStandardError $ErrLog `
        -PassThru

    Start-Sleep -Milliseconds 900
    $r = Get-BinaryRevision -Path $Exe
    Write-RunLog ("started pid={0} revision={1} dirty={2}" -f $proc.Id, $r.Revision, $r.Modified)
    Show-Status
}

function Stop-Server {
    $proc = Get-Listener
    if (-not $proc) {
        Write-Host "Nothing listening on port $Port." -ForegroundColor Yellow
        return
    }
    Write-Host ("Stopping PID {0} ({1}) ..." -f $proc.Id, $proc.ProcessName)
    Stop-Process -Id $proc.Id -Force -Confirm:$false
    Write-RunLog ("stopped pid={0}" -f $proc.Id)
    Start-Sleep -Milliseconds 600
}

switch ($Action) {
    'start'   { Start-Server }
    'stop'    { Stop-Server }
    'restart' { Stop-Server; Start-Server }
    'status'  { Show-Status }
}
