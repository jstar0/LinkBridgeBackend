param(
  [Parameter(Mandatory = $false)]
  [string]$HostName = "103.40.13.96",

  [Parameter(Mandatory = $false)]
  [int]$Port = 22,

  [Parameter(Mandatory = $false)]
  [string]$UserName = "root",

  [Parameter(Mandatory = $false)]
  [string]$ServiceName = "linkbridge",

  [Parameter(Mandatory = $false)]
  [string]$RemoteTmpPath = "/tmp/linkbridge-backend",

  [Parameter(Mandatory = $false)]
  [string]$RemoteInstallPath = "/opt/linkbridge-backend",

  [Parameter(Mandatory = $false)]
  [string]$KeyPath
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Assert-CommandExists {
  param([Parameter(Mandatory = $true)][string]$Name)
  if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
    throw "Missing command '$Name'. Please install/enable it (Go + OpenSSH) and try again."
  }
}

function Invoke-External {
  param(
    [Parameter(Mandatory = $true)][string]$Exe,
    [Parameter(Mandatory = $false)][string[]]$Args = @()
  )
  Write-Host ("`n> " + $Exe + " " + ($Args -join " "))
  & $Exe @Args
  if ($LASTEXITCODE -ne 0) {
    throw "Command failed with exit code $LASTEXITCODE: $Exe"
  }
}

Assert-CommandExists "go"
Assert-CommandExists "ssh"
Assert-CommandExists "scp"

$repoRoot = $PSScriptRoot
Push-Location $repoRoot
try {
  $outFile = Join-Path $repoRoot "linkbridge-backend"

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
    Invoke-External -Exe "go" -Args @("build", "-o", $outFile, "./cmd/api")
  }
  finally {
    $env:CGO_ENABLED = $oldEnv.CGO_ENABLED
    $env:GOOS = $oldEnv.GOOS
    $env:GOARCH = $oldEnv.GOARCH
  }

  $target = "{0}@{1}:{2}" -f $UserName, $HostName, $RemoteTmpPath

  Write-Host "=== Upload to server ==="
  $scpArgs = @()
  if ($KeyPath) { $scpArgs += @("-i", $KeyPath) }
  if ($Port -and $Port -ne 22) { $scpArgs += @("-P", "$Port") }
  $scpArgs += @($outFile, $target)
  Invoke-External -Exe "scp" -Args $scpArgs

  Write-Host "=== Deploy service on server ==="
  $sshArgs = @()
  if ($KeyPath) { $sshArgs += @("-i", $KeyPath) }
  if ($Port -and $Port -ne 22) { $sshArgs += @("-p", "$Port") }
  $sshArgs += @("{0}@{1}" -f $UserName, $HostName)

  $remoteScript = @"
systemctl stop $ServiceName || true
mv $RemoteTmpPath $RemoteInstallPath
chmod +x $RemoteInstallPath
systemctl start $ServiceName
sleep 2
systemctl status $ServiceName --no-pager
"@.Trim()

  $sshArgs += @($remoteScript)
  Invoke-External -Exe "ssh" -Args $sshArgs

  Write-Host "=== Cleanup local binary ==="
  if (Test-Path $outFile) { Remove-Item -Force $outFile }

  Write-Host "=== Done ==="
  Write-Host "If you are using password auth, scp/ssh will prompt for it in the terminal."
}
finally {
  Pop-Location
}

