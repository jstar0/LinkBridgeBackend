param(
  [string]$EnvFile = ".env.local",
  [string]$HttpAddr = ""
)

$ErrorActionPreference = 'Stop'

function Import-DotEnvFile([string]$Path) {
  if (-not (Test-Path $Path)) {
    Write-Host "No env file found at $Path (skipping)."
    return
  }

  Get-Content -LiteralPath $Path | ForEach-Object {
    $line = $_.Trim()
    if ($line.Length -eq 0) { return }
    if ($line.StartsWith('#')) { return }
    if ($line.StartsWith('export ')) { $line = $line.Substring(7).Trim() }

    $idx = $line.IndexOf('=')
    if ($idx -lt 1) { return }

    $key = $line.Substring(0, $idx).Trim()
    $val = $line.Substring($idx + 1).Trim()
    if ($key.Length -eq 0) { return }

    if (($val.StartsWith('"') -and $val.EndsWith('"')) -or ($val.StartsWith("'") -and $val.EndsWith("'"))) {
      $val = $val.Substring(1, $val.Length - 2)
    }

    Set-Item -Path ("Env:" + $key) -Value $val
  }
}

Import-DotEnvFile $EnvFile

if ($HttpAddr -ne "") {
  $env:HTTP_ADDR = $HttpAddr
}

Write-Host "Starting LinkBridge backend..."
Write-Host ("HTTP_ADDR=" + $(if ($null -ne $env:HTTP_ADDR) { $env:HTTP_ADDR } else { "" }))
Write-Host ("DATABASE_URL=" + $(if ($null -ne $env:DATABASE_URL) { $env:DATABASE_URL } else { "" }))
Write-Host ("WECHAT_APPID=" + $(if ($null -ne $env:WECHAT_APPID) { $env:WECHAT_APPID } else { "" }))
Write-Host ("WECHAT_CALL_SUBSCRIBE_TEMPLATE_ID=" + $(if ($null -ne $env:WECHAT_CALL_SUBSCRIBE_TEMPLATE_ID) { $env:WECHAT_CALL_SUBSCRIBE_TEMPLATE_ID } else { "" }))

go run ./cmd/api
