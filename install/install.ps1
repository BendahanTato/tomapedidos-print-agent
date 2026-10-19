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
$binDir = "$env:ProgramFiles\Tomapedidos\PrintAgent"
$dest = "$binDir\print-agent.exe"
$dataRoot = "$env:ProgramData\Tomapedidos"
$configDir = "$dataRoot\config"
$dataDir = "$dataRoot\data"
$logsDir = "$dataRoot\logs"
$config = "$configDir\printers.json"
$jobsPath = "$dataDir\jobs.db"
$serviceName = "com.tomapedidos.print-agent"
$serviceAccount = "NT SERVICE\$serviceName"
$legacyBinDir = "$env:LOCALAPPDATA\tomapedidos"
$legacyDest = "$legacyBinDir\print-agent.exe"
$legacyConfigDir = "$env:APPDATA\tomapedidos"
$legacyConfig = "$legacyConfigDir\printers.json"

function Wait-ForAgentHealth {
  for ($attempt = 0; $attempt -lt 30; $attempt++) {
    try {
      Invoke-RestMethod -Uri "http://127.0.0.1:4510/health" -TimeoutSec 1 -ErrorAction Stop | Out-Null
      return $true
    } catch {
      if ($attempt -lt 29) {
        Start-Sleep -Seconds 1
      }
    }
  }
  return $false
}

function Restore-PreviousService {
  param(
    [string]$Name,
    [string]$BinaryPath,
    [string]$InstalledBinary,
    [string]$BinaryBackup
  )

  try {
    $service = Get-Service -Name $Name -ErrorAction Stop
    if ($service.Status -ne "Stopped") {
      Stop-Service -Name $Name -ErrorAction Stop
      $service = Get-Service -Name $Name -ErrorAction Stop
      $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(60))
    }
    if ($BinaryBackup -and (Test-Path $BinaryBackup)) {
      Copy-Item -Force -Path $BinaryBackup -Destination $InstalledBinary
    }
    & sc.exe config $Name binPath= $BinaryPath obj= LocalSystem password= ""
    $rollbackScExit = $LASTEXITCODE
    if ($rollbackScExit -ne 0) {
      throw "sc.exe config rollback failed with exit code $rollbackScExit."
    }
    Start-Service -Name $Name -ErrorAction Stop
    return (Wait-ForAgentHealth)
  } catch {
    Write-Host "Rollback error: $($_.Exception.Message)"
    return $false
  }
}

if ($Version -eq "latest") {
  $releaseBase = "https://github.com/${repo}/releases/latest/download"
} else {
$releaseBase = "https://github.com/${repo}/releases/download/${Version}"
}
$url = "${releaseBase}/${name}"
$checksumsUrl = "${releaseBase}/SHA256SUMS"
# Stage downloads under Program Files so a standard user cannot replace the
# executable between checksum verification and installation.
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
$tempFile = "$binDir\print-agent.download.exe"
$checksumsFile = "$binDir\SHA256SUMS.download"

Write-Host "=== downloading print-agent $Version for windows/$arch"
try {
  Invoke-WebRequest -Uri $url -OutFile $tempFile -UseBasicParsing
  Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsFile -UseBasicParsing

  $checksumEntry = Get-Content -Path $checksumsFile | ForEach-Object {
    if ($_ -match '^\s*([A-Fa-f0-9]{64})\s+\*?(.+?)\s*$') {
      [PSCustomObject]@{ Hash = $Matches[1]; Name = $Matches[2] }
    }
  } | Where-Object { $_.Name -eq $name } | Select-Object -First 1

  if (-not $checksumEntry) {
    throw "SHA256SUMS does not contain an entry for $name."
  }

  $actualHash = (Get-FileHash -Algorithm SHA256 -Path $tempFile).Hash
  if ($actualHash -ine $checksumEntry.Hash) {
    throw "SHA256 verification failed for $name (expected $($checksumEntry.Hash), got $actualHash)."
  }
} catch {
  Remove-Item -Force -ErrorAction SilentlyContinue $tempFile, $checksumsFile
  throw "Error: could not verify the downloaded print-agent. $($_.Exception.Message)"
}
Remove-Item -Force -ErrorAction SilentlyContinue $checksumsFile

# Stop an existing service before backing up or copying its SQLite database.
$service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
$hadService = $null -ne $service
$legacyJobsPattern = "$legacyConfigDir\jobs.db*"
$hasLegacyFiles = (Test-Path $legacyDest) -or (Test-Path $legacyConfig) -or (Test-Path -Path $legacyJobsPattern)
$isMigration = $hadService -or $hasLegacyFiles
$backupDir = $null
$oldBinPath = $null
$installedBinaryBackup = $null

if ($hadService) {
  if ($service.Status -ne "Stopped") {
    Stop-Service -Name $serviceName -ErrorAction Stop
    $service = Get-Service -Name $serviceName -ErrorAction Stop
    $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(60))
  }
}

if ($isMigration) {
  $timestamp = Get-Date -Format "yyyyMMdd-HHmmss-fffffff"
  $backupDir = "$dataRoot\backups\migration-$timestamp"
  New-Item -ItemType Directory -Force -Path $backupDir | Out-Null

  if (Test-Path $legacyConfig) {
    Copy-Item -Path $legacyConfig -Destination "$backupDir\printers.json" -Force
  }
  if (Test-Path -Path $legacyJobsPattern) {
    Copy-Item -Path $legacyJobsPattern -Destination $backupDir -Force
  }

  if ($hadService) {
    # Keep a human-readable dump for support, but DO NOT parse it: the labels
    # emitted by "sc.exe qc" are localized (Spanish Windows prints
    # RUTA_DE_ARCHIVO_BINARIO instead of BINARY_PATH_NAME), so reading the
    # previous binPath from WMI keeps the migration working on any locale.
    $serviceBeforePath = "$backupDir\service-before.txt"
    & sc.exe qc $serviceName | Out-File -FilePath $serviceBeforePath

    $existing = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'"
    if ($null -eq $existing) {
      throw "Could not read the existing configuration of service $serviceName. Backup: $backupDir"
    }
    $oldBinPath = $existing.PathName
    if ([string]::IsNullOrWhiteSpace($oldBinPath)) {
      throw "The previous service binPath is empty for $serviceName. Backup: $backupDir"
    }
    if (Test-Path $dest) {
      $installedBinaryBackup = "$backupDir\program-files-before.exe"
      Copy-Item -Force -Path $dest -Destination $installedBinaryBackup
    }
  }
}

New-Item -ItemType Directory -Force -Path $binDir | Out-Null
New-Item -ItemType Directory -Force -Path $configDir | Out-Null
New-Item -ItemType Directory -Force -Path $dataDir | Out-Null
New-Item -ItemType Directory -Force -Path $logsDir | Out-Null

# Copy the verified download. Keep the legacy executable untouched for rollback.
Copy-Item -Force -Path $tempFile -Destination $dest
Remove-Item -Force -ErrorAction SilentlyContinue $tempFile

if ($isMigration -and (Test-Path -Path $legacyJobsPattern)) {
  Copy-Item -Path $legacyJobsPattern -Destination $dataDir -Force
}

Write-Host "=== generating config"
if ((Test-Path $legacyConfig) -and -not (Test-Path $config)) {
  Copy-Item -Path $legacyConfig -Destination $config -Force
}
if (-not (Test-Path $config)) {
  & "$dest" init-config --config "$config"
  $initExit = $LASTEXITCODE
  if ($initExit -ne 0) {
    throw "Could not generate the starter config (print-agent exit code $initExit)."
  }
  Write-Host "=== starter config written to $config — edit it before starting"
}

# Validate the existing JSON before changing its queue path; malformed config
# must fail visibly rather than silently starting with an unintended database.
try {
  $cfgJson = Get-Content -Raw -Path $config | ConvertFrom-Json
  if ($null -eq $cfgJson -or $cfgJson -isnot [PSCustomObject]) {
    throw "The config root must be a JSON object."
  }
  if ($null -eq $cfgJson.queue) {
    $cfgJson | Add-Member -MemberType NoteProperty -Name "queue" -Value ([PSCustomObject]@{})
  } elseif ($cfgJson.queue -isnot [PSCustomObject]) {
    throw "The config queue value must be a JSON object."
  }
  if ($cfgJson.queue.PSObject.Properties["persist_path"]) {
    $cfgJson.queue.persist_path = $jobsPath
  } else {
    $cfgJson.queue | Add-Member -MemberType NoteProperty -Name "persist_path" -Value $jobsPath
  }
  $cfgJson | ConvertTo-Json -Depth 10 | Set-Content -Path $config
} catch {
  throw "Could not validate or update persist_path in $config. The service was not reconfigured. $($_.Exception.Message)"
}

Write-Host "=== registering as a service"
$installError = $null
try {
  & "$dest" install --config "$config"
  $installExit = $LASTEXITCODE
  if ($installExit -ne 0) {
    if (-not $hadService) {
      throw "print-agent install failed with exit code $installExit."
    }

    Write-Host "print-agent install failed (exit code $installExit); trying an in-place sc.exe config fallback."
    $newBinPath = "`"$dest`" start --config `"$config`""
    $accountArg = "`"$serviceAccount`""
    & sc.exe config $serviceName binPath= $newBinPath start= auto obj= $accountArg password= ""
    $fallbackExit = $LASTEXITCODE
    if ($fallbackExit -ne 0) {
      throw "sc.exe config fallback failed with exit code $fallbackExit."
    }
  }

  # The virtual service account is resolvable after the service is registered.
  # Grant directory-level rights so SQLite can create its WAL/SHM sidecars.
  $serviceGrant = "${serviceAccount}:(OI)(CI)F"
  foreach ($directory in @($configDir, $dataDir, $logsDir)) {
    & icacls.exe $directory /grant $serviceGrant "Users:(OI)(CI)M"
    $aclExit = $LASTEXITCODE
    if ($aclExit -ne 0) {
      throw "Could not set permissions on $directory (icacls.exe exit code $aclExit)."
    }
  }
} catch {
  $installError = $_.Exception.Message
}

if ($installError) {
  if ($hadService) {
    Write-Host "Service reconfiguration failed: $installError"
    $rollbackHealthy = Restore-PreviousService -Name $serviceName -BinaryPath $oldBinPath -InstalledBinary $dest -BinaryBackup $installedBinaryBackup
    if ($rollbackHealthy) {
      throw "Migration failed; the previous service was restored and passed the health check. Backup: $backupDir"
    }
    Write-Host "Automatic rollback did not pass the health check. Backup: $backupDir"
    Write-Host "Manual recovery (run in an elevated PowerShell):"
    Write-Host "  Stop-Service -Name $serviceName -ErrorAction SilentlyContinue"
    if ($installedBinaryBackup) {
      Write-Host "  Copy-Item -Force `"$installedBinaryBackup`" `"$dest`""
    }
    Write-Host "  `$oldBinPath = @'"
    Write-Host $oldBinPath
    Write-Host "'@"
    Write-Host "  sc.exe config $serviceName binPath= `$oldBinPath obj= LocalSystem password= `"`""
    Write-Host "  Start-Service -Name $serviceName"
    throw "Migration and rollback health check failed. Use the backup and manual commands above."
  }
  throw "Could not install the service: $installError"
}

Write-Host "=== starting service"
$startError = $null
try {
  Start-Service -Name $serviceName -ErrorAction Stop
} catch {
  $startError = $_.Exception.Message
  Write-Host "Start-Service reported: $startError"
}

$healthy = Wait-ForAgentHealth
if (-not $healthy) {
  if ($hadService) {
    Write-Host "The new service did not pass the HTTP health check. Restoring the previous service."
    $rollbackHealthy = Restore-PreviousService -Name $serviceName -BinaryPath $oldBinPath -InstalledBinary $dest -BinaryBackup $installedBinaryBackup
    if ($rollbackHealthy) {
      throw "Migration rolled back: the previous service was restored and passed the health check. Backup: $backupDir"
    }
    Write-Host "Automatic rollback did not pass the health check. Backup: $backupDir"
    Write-Host "Manual recovery (run in an elevated PowerShell):"
    Write-Host "  Stop-Service -Name $serviceName -ErrorAction SilentlyContinue"
    if ($installedBinaryBackup) {
      Write-Host "  Copy-Item -Force `"$installedBinaryBackup`" `"$dest`""
    }
    Write-Host "  `$oldBinPath = @'"
    Write-Host $oldBinPath
    Write-Host "'@"
    Write-Host "  sc.exe config $serviceName binPath= `$oldBinPath obj= LocalSystem password= `"`""
    Write-Host "  Start-Service -Name $serviceName"
    throw "Neither the migrated nor restored service passed its health check. Use the backup and manual commands above."
  }
  throw "The newly installed service did not pass http://127.0.0.1:4510/health. Check the service and config before retrying; no legacy files were removed."
}

# Only remove the old user-writable install after the new service is healthy.
if (Test-Path $legacyDest) {
  try {
    Remove-Item -Force -Path $legacyDest
  } catch {
    Write-Host "WARNING: could not remove the old executable ${legacyDest}: $($_.Exception.Message)"
  }
}
if (Test-Path $legacyConfig) {
  try {
    Remove-Item -Force -Path $legacyConfig
  } catch {
    Write-Host "WARNING: could not remove the old config ${legacyConfig}: $($_.Exception.Message)"
  }
}
if (Test-Path -Path $legacyJobsPattern) {
  try {
    Remove-Item -Force -Path $legacyJobsPattern
  } catch {
    Write-Host "WARNING: could not remove the old queue files in ${legacyConfigDir}: $($_.Exception.Message)"
  }
}

Write-Host "=== done"
Write-Host "Agent installed as a Windows service and passed its HTTP health check."
Write-Host "  Panel:  http://127.0.0.1:4510"
Write-Host "  Config: $config"
Write-Host "  Queue:  $jobsPath"
Write-Host "  Logs:   $logsDir"
Write-Host "  Binary: $dest"
Write-Host "  Account: $serviceAccount"
if ($backupDir) {
  Write-Host "  Migration backup: $backupDir"
}
Write-Host ""
Write-Host "Manage:"
Write-Host "  & '$dest' status-svc"
Write-Host "  & '$dest' stop-svc"
Write-Host "  & '$dest' start-svc"
Write-Host "  & '$dest' uninstall"
