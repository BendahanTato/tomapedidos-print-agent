# Quick-install script for Windows.
# Download the binary from GitHub Releases and install as a Windows service.
param(
  [string]$Version = "latest"
)

$repo = "BendahanTato/tomapedidos-print-agent"
$arch = if ([System.Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
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
Invoke-WebRequest -Uri $url -OutFile "$env:TEMP\print-agent.exe"

New-Item -ItemType Directory -Force -Path $binDir | Out-Null
New-Item -ItemType Directory -Force -Path $configDir | Out-Null
Move-Item -Force "$env:TEMP\print-agent.exe" $dest

Write-Host "=== generating config"
if (-not (Test-Path $config)) {
  & $dest init-config --config $config
  Write-Host "=== starter config written to $config — edit it before starting"
}

# Fix persist_path to an absolute path.
if (Get-Command python3 -ErrorAction SilentlyContinue) {
  python3 -c "import json,sys; cfg=json.load(open(r'$config')); cfg['queue']['persist_path']=r'$configDir\jobs.db'; json.dump(cfg,open(r'$config','w'),indent=2)" 2>$null
}

Write-Host "=== registering as a service"
& $dest install --config $config

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
