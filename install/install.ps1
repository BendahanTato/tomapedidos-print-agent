# Quick-install script for Windows.
# Download the binary from GitHub Releases and install as a Windows service.
param(
  [string]$Version = "latest"
)

$ErrorActionPreference = "Stop"

# Ensure TLS 1.2 is enabled for GitHub downloads on older PowerShell versions
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {}

$repo = "BendahanTato/tomapedidos-print-agent"
$isArm = $env:PROCESSOR_ARCHITECTURE -eq "ARM64"
$arch = if ($isArm) { "arm64" } else { "amd64" }
$name = "print-agent-windows-$arch.exe"
$binDir = "$env:LOCALAPPDATA\tomapedidos"
$dest = "$binDir\print-agent.exe"
$configDir = "$env:APPDATA\tomapedidos"
$config = "$configDir\printers.json"

if ($Version -eq "latest") {
  $url = "https://github.com/${repo}/releases/latest/download/${name}"
} else {
  $url = "https://github.com/${repo}/releases/download/${Version}/${name}"
}

Write-Host "=== downloading print-agent $Version for windows/$arch"
Invoke-WebRequest -Uri $url -OutFile "$env:TEMP\print-agent.exe" -UseBasicParsing

New-Item -ItemType Directory -Force -Path $binDir | Out-Null
New-Item -ItemType Directory -Force -Path $configDir | Out-Null
Move-Item -Force "$env:TEMP\print-agent.exe" $dest

Write-Host "=== generating config"
if (-not (Test-Path $config)) {
  & "$dest" init-config --config "$config"
  Write-Host "=== starter config written to $config — edit it before starting"
}

# Fix persist_path to an absolute path using native PowerShell JSON
if (Test-Path $config) {
  try {
    $cfgJson = Get-Content -Raw -Path $config | ConvertFrom-Json
    if (-not $cfgJson.queue) {
      $cfgJson | Add-Member -MemberType NoteProperty -Name "queue" -Value ([PSCustomObject]@{})
    }
    $cfgJson.queue.persist_path = "$configDir\jobs.db"
    $cfgJson | ConvertTo-Json -Depth 10 | Set-Content -Path $config
  } catch {
    Write-Host "Notice: could not auto-update persist_path: $_"
  }
}

Write-Host "=== registering as a service"
& "$dest" install --config "$config"
& "$dest" start-svc

Write-Host "=== done"
Write-Host "Agent installed as a Windows service and started."
Write-Host "  Panel:  http://127.0.0.1:4510"
Write-Host "  Config: $config"
Write-Host "  Binary: $dest"
Write-Host ""
Write-Host "Manage:"
Write-Host "  & '$dest' status-svc"
Write-Host "  & '$dest' stop-svc"
Write-Host "  & '$dest' start-svc"
Write-Host "  & '$dest' uninstall"
