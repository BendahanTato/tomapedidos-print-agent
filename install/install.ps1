# Quick-install script for Windows.
# Download the binary from GitHub Releases and install as a Windows service.
param(
  [string]$Version = "latest"
)

$repo = "BendahanTato/tomapedidos-print-agent"
$arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
$name = "print-agent-windows-$arch.exe"
$dest = "$env:APPDATA\tomapedidos\print-agent.exe"
$configDir = "$env:APPDATA\tomapedidos"
$config = "$configDir\printers.json"

if ($Version -eq "latest") {
  $url = "https://github.com/${repo}/releases/latest/download/${name}"
} else {
  $url = "https://github.com/${repo}/releases/download/${Version}/${name}"
}

Write-Host "=== downloading print-agent $Version for windows/$arch"
Invoke-WebRequest -Uri $url -OutFile "$env:TEMP\print-agent.exe"

New-Item -ItemType Directory -Force -Path $configDir | Out-Null
Move-Item -Force "$env:TEMP\print-agent.exe" $dest

Write-Host "=== generating config"
if (-not (Test-Path $config)) {
  & $dest init-config --config $config
  Write-Host "=== starter config written to $config — edit it before starting"
}

Write-Host "=== registering as a service"
& $dest install --config $config

Write-Host "=== done"
Write-Host "Agent installed as a Windows service and started."
Write-Host "  Panel:  http://127.0.0.1:4510"
Write-Host "  Config: $config"
Write-Host ""
Write-Host "Manage:"
Write-Host "  sc start com.tomapedidos.print-agent"
Write-Host "  sc stop  com.tomapedidos.print-agent"
Write-Host "  $dest uninstall"
