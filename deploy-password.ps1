param(
  [Parameter(Mandatory = $false)]
  [string]$CredFile = "..\\mima.txt",

  [Parameter(Mandatory = $false)]
  [string]$HostName,

  [Parameter(Mandatory = $false)]
  [int]$Port = 22,

  [Parameter(Mandatory = $false)]
  [string]$UserName,

  [Parameter(Mandatory = $false)]
  [string]$Password,

  [Parameter(Mandatory = $false)]
  [string]$ServiceName = "linkbridge",

  [Parameter(Mandatory = $false)]
  [string]$RemoteTmpPath = "/tmp/linkbridge-backend",

  [Parameter(Mandatory = $false)]
  [string]$RemoteInstallPath = "/opt/linkbridge-backend"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-CommandExists {
  param([Parameter(Mandatory = $true)][string]$Name)
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing command '$Name'."
  }
}

function Ensure-WinSCP {
  $toolsDir = Join-Path $PSScriptRoot ".tools\\winscp"
  $exeCom = Join-Path $toolsDir "WinSCP.com"
  $exeGui = Join-Path $toolsDir "WinSCP.exe"
  if ((Test-Path $exeCom) -and (Test-Path $exeGui)) {
    return $exeCom
  }

  New-Item -ItemType Directory -Force -Path $toolsDir | Out-Null

  # Official "downloading..." page (contains a time-limited CDN link to the ZIP).
  # Note: We intentionally fetch the HTML page first, then resolve the real ZIP URL,
  # otherwise we may accidentally download HTML and treat it as a ZIP.
  $downloadPageUrl = "https://winscp.net/download/WinSCP-6.3.6-Portable.zip"
  Write-Host "=== Resolve WinSCP portable ZIP URL ==="
  $page = Invoke-WebRequest -Uri $downloadPageUrl -UseBasicParsing
  $html = $page.Content
  $zipUrl = $null
  $m = [regex]::Match($html, '(https?://)?cdn\.winscp\.net/files/WinSCP-[^"\s>]+-Portable\.zip\?secure=[^"\s>]+', 'IgnoreCase')
  if ($m.Success) {
    $zipUrl = $m.Value
    if (-not $zipUrl.StartsWith("http")) {
      $zipUrl = "https://" + $zipUrl
    }
  }
  if (-not $zipUrl) {
    $m2 = [regex]::Match($html, 'https?://sourceforge\.net/projects/winscp/files/WinSCP/[^"\s>]+/WinSCP-[^"\s>]+-Portable\.zip/download', 'IgnoreCase')
    if ($m2.Success) {
      $zipUrl = $m2.Value
    }
  }
  if (-not $zipUrl) {
    throw "Failed to resolve WinSCP portable ZIP URL from $downloadPageUrl"
  }

  # Download ZIP and unzip only WinSCP.com for scripting.
  $zipPath = Join-Path $toolsDir "winscp-portable.zip"

  Write-Host "=== Download WinSCP portable CLI ==="
  Invoke-WebRequest -Uri $zipUrl -OutFile $zipPath -UseBasicParsing

  Write-Host "=== Extract WinSCP.com + WinSCP.exe ==="
  $sig = [System.IO.File]::ReadAllBytes($zipPath)[0..1]
  if (-not ($sig[0] -eq 0x50 -and $sig[1] -eq 0x4B)) {
    $head = [System.Text.Encoding]::ASCII.GetString([System.IO.File]::ReadAllBytes($zipPath)[0..120])
    $head = $head -replace "`r", "" -replace "`n", " "
    throw ("Downloaded file is not a ZIP (missing PK signature). Head: " + $head)
  }

  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [System.IO.Compression.ZipFile]::OpenRead($zipPath)
  try {
    $foundCom = $false
    $foundExe = $false
    foreach ($entry in $zip.Entries) {
      if ($entry.Name -ieq "WinSCP.com") {
        [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $exeCom, $true)
        $foundCom = $true
        continue
      }
      if ($entry.Name -ieq "WinSCP.exe") {
        [System.IO.Compression.ZipFileExtensions]::ExtractToFile($entry, $exeGui, $true)
        $foundExe = $true
        continue
      }
    }
    if (-not $foundCom -or -not $foundExe) {
      throw "WinSCP portable ZIP did not contain required files (WinSCP.com/WinSCP.exe)."
    }
  }
  finally {
    $zip.Dispose()
  }

  Remove-Item -Force $zipPath -ErrorAction SilentlyContinue

  if (-not (Test-Path $exeCom) -or -not (Test-Path $exeGui)) {
    throw "WinSCP.com/WinSCP.exe not found after extraction."
  }
  return $exeCom
}

function Read-Creds {
  param([Parameter(Mandatory = $true)][string]$Path)
  $full = Resolve-Path $Path
  $lines = Get-Content $full | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
  if ($lines.Count -lt 3) {
    throw "Credential file must contain at least 3 non-empty lines: host, username, password."
  }
  return @{
    Host = $lines[0]
    User = $lines[1]
    Pass = $lines[2]
  }
}

Assert-CommandExists "go"

if (-not $HostName -or -not $UserName -or -not $Password) {
  $c = Read-Creds -Path (Join-Path $PSScriptRoot $CredFile)
  if (-not $HostName) { $HostName = $c.Host }
  if (-not $UserName) { $UserName = $c.User }
  if (-not $Password) { $Password = $c.Pass }
}

if (-not $HostName -or -not $UserName -or -not $Password) {
  throw "Missing HostName/UserName/Password"
}

$winscp = Ensure-WinSCP

Push-Location $PSScriptRoot
try {
  $outFile = Join-Path $PSScriptRoot "linkbridge-backend"

  Write-Host "=== Build linux amd64 binary ==="
  $oldEnv = @{
    CGO_ENABLED = $env:CGO_ENABLED
    GOOS        = $env:GOOS
    GOARCH      = $env:GOARCH
  }
  try {
    $env:CGO_ENABLED = "0"
    $env:GOOS = "linux"
    $env:GOARCH = "amd64"
    if (Test-Path $outFile) { Remove-Item -Force $outFile }
    & go build -o $outFile ./cmd/api
    if ($LASTEXITCODE -ne 0) { throw "go build failed" }
  }
  finally {
    $env:CGO_ENABLED = $oldEnv.CGO_ENABLED
    $env:GOOS = $oldEnv.GOOS
    $env:GOARCH = $oldEnv.GOARCH
  }

  Write-Host "=== Upload + deploy via WinSCP (password auth) ==="
  $tmp = Join-Path $env:TEMP ("winscp-deploy-" + [Guid]::NewGuid().ToString("N") + ".txt")
  $passFile = Join-Path $env:TEMP ("winscp-pass-" + [Guid]::NewGuid().ToString("N") + ".txt")

  # SECURITY NOTE:
  # - Password is stored only in a temporary file (deleted after), and passed to WinSCP using -passwordsfromfiles.
  # - The WinSCP script does not contain the password, so we can safely print WinSCP output on failure.
  $sessionUrl = "sftp://{0}@{1}:{2}/" -f $UserName, $HostName, $Port

  # WinSCP reads only first line; keep it simple.
  Set-Content -Path $passFile -Value $Password -Encoding UTF8

  $script = @"
option batch abort
option confirm off
open $sessionUrl -password="$passFile" -passwordsfromfiles -hostkey="*"
put `"$outFile`" $RemoteTmpPath
call systemctl stop $ServiceName || true
call mv $RemoteTmpPath $RemoteInstallPath
call chmod +x $RemoteInstallPath
call systemctl start $ServiceName
call sleep 2
call systemctl status $ServiceName --no-pager
exit
"@
  Set-Content -Path $tmp -Value $script -Encoding ASCII

  try {
    $out = & $winscp "/script=$tmp"
    if ($LASTEXITCODE -ne 0) {
      if ($out) { Write-Host ($out -join "`n") }
      throw "WinSCP deploy failed (exit=$LASTEXITCODE)."
    }
  }
  finally {
    Remove-Item -Force $tmp -ErrorAction SilentlyContinue
    Remove-Item -Force $passFile -ErrorAction SilentlyContinue
  }

  Write-Host "=== Cleanup local binary ==="
  if (Test-Path $outFile) { Remove-Item -Force $outFile }

  Write-Host "=== Done ==="
}
finally {
  Pop-Location
}
