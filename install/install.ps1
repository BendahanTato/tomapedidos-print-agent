# Interactive step-by-step installer for TomaPedidos Print Agent (Windows).
# Guides the operator through permissions, binary install, local config,
# printer detection/assignment, service registration, health verification,
# and generates a structured install log + optional server report.
param(
  [string]$Version = "latest",
  [string]$LocalBinary = "",
  [string]$ReportUrl = "",
  [switch]$GuiSetup,
  [switch]$WebSetup,
  [switch]$NonInteractive
)

$ErrorActionPreference = "Stop"

try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
} catch {}

$repo = "BendahanTato/tomapedidos-print-agent"
$isArm = $env:PROCESSOR_ARCHITECTURE -eq "ARM64"
$arch = if ($isArm) { "arm64" } else { "amd64" }
$releaseAssetName = "print-agent-windows-$arch.exe"

$binDir = "$env:ProgramFiles\Tomapedidos\PrintAgent"
$dest = "$binDir\print-agent.exe"
$dataRoot = "$env:ProgramData\Tomapedidos"
$configDir = "$dataRoot\config"
$dataDir = "$dataRoot\data"
$logsDir = "$dataRoot\logs"
$configPath = "$configDir\printers.json"
$jobsPath = "$dataDir\jobs.db"
$agentLogPath = "$logsDir\print-agent.log"
$serviceName = "com.tomapedidos.print-agent"
$serviceAccount = "NT SERVICE\$serviceName"

$legacyBinDir = "$env:LOCALAPPDATA\tomapedidos"
$legacyDest = "$legacyBinDir\print-agent.exe"
$legacyConfigDir = "$env:APPDATA\tomapedidos"
$legacyConfig = "$legacyConfigDir\printers.json"

$timestampTag = Get-Date -Format "yyyyMMdd-HHmmss"
$logFile = "$logsDir\install.log"
$archiveLogFile = "$logsDir\install-$timestampTag.log"
$reportFile = "$logsDir\install-report.json"

# Ensure log directory exists early (fallback to TEMP if ProgramData is blocked).
try {
  New-Item -ItemType Directory -Force -Path $logsDir | Out-Null
} catch {
  $logFile = "$env:TEMP\tomapedidos-install.log"
  $archiveLogFile = "$env:TEMP\tomapedidos-install-$timestampTag.log"
  $reportFile = "$env:TEMP\tomapedidos-install-report.json"
}

$supportCode = "TP-" + ([guid]::NewGuid().ToString("N").Substring(0, 4).ToUpperInvariant())
$script:Report = [ordered]@{
  support_code      = $supportCode
  status            = "in_progress"
  started_at        = (Get-Date).ToString("o")
  finished_at       = $null
  hostname          = $env:COMPUTERNAME
  windows_user      = "$env:USERDOMAIN\$env:USERNAME"
  is_admin          = $false
  arch              = $arch
  target_version    = $Version
  installed_version = ""
  tenant_id         = ""
  branch_id         = ""
  port              = 4510
  printers          = @()
  failed_step       = ""
  error             = ""
  log_file          = $logFile
  steps             = [System.Collections.ArrayList]::new()
}
$script:CurrentStep = "0_inicio"

function Write-Log {
  param(
    [string]$Level = "INFO",
    [string]$Message
  )
  $line = "[{0}] [{1}] {2}" -f (Get-Date -Format "yyyy-MM-dd HH:mm:ss"), $Level, $Message
  switch ($Level) {
    "ERROR" { Write-Host $line -ForegroundColor Red }
    "WARN"  { Write-Host $line -ForegroundColor Yellow }
    "OK"    { Write-Host $line -ForegroundColor Green }
    default { Write-Host $line }
  }
  foreach ($target in @($logFile, $archiveLogFile)) {
    try {
      Add-Content -Path $target -Value $line -Encoding UTF8
    } catch {}
  }
}

function Add-StepResult {
  param(
    [string]$StepId,
    [string]$Title,
    [string]$Status,
    [string]$Detail = ""
  )
  $entry = [ordered]@{
    step   = $StepId
    title  = $Title
    status = $Status
    detail = $Detail
    at     = (Get-Date).ToString("o")
  }
  [void]$script:Report.steps.Add($entry)
  $tag = switch ($Status) {
    "ok"     { "OK" }
    "warn"   { "WARN" }
    "failed" { "ERROR" }
    default  { "INFO" }
  }
  Write-Log -Level $tag -Message "[$StepId] $Title - $Detail"
}

function Write-Utf8NoBom {
  param(
    [string]$Path,
    [string]$Content
  )
  $utf8NoBom = New-Object System.Text.UTF8Encoding($false)
  [System.IO.File]::WriteAllText($Path, $Content, $utf8NoBom)
}

function Save-InstallReport {
  param([string]$TargetReportUrl)
  $script:Report.finished_at = (Get-Date).ToString("o")
  $json = $script:Report | ConvertTo-Json -Depth 10
  try {
    Write-Utf8NoBom -Path $reportFile -Content $json
    Write-Log -Level "INFO" -Message "Reporte local guardado en $reportFile"
  } catch {
    Write-Log -Level "WARN" -Message "No se pudo guardar el reporte local: $($_.Exception.Message)"
  }

  if (-not [string]::IsNullOrWhiteSpace($TargetReportUrl)) {
    Write-Log -Level "INFO" -Message "Enviando informe de instalacion al servidor: $TargetReportUrl"
    try {
      $payloadObj = [ordered]@{
        report         = $script:Report
        log_tail       = @(Get-Content -Path $logFile -Tail 60 -ErrorAction SilentlyContinue)
        agent_log_tail = @(Get-Content -Path $agentLogPath -Tail 30 -ErrorAction SilentlyContinue)
      }
      $payloadJson = $payloadObj | ConvertTo-Json -Depth 12
      $resp = Invoke-RestMethod -Uri $TargetReportUrl -Method Post -ContentType "application/json; charset=utf-8" -Body $payloadJson -TimeoutSec 10 -ErrorAction Stop
      Write-Log -Level "OK" -Message "Informe enviado al servidor correctamente."
    } catch {
      Write-Log -Level "WARN" -Message "No se pudo enviar el informe al servidor ($TargetReportUrl): $($_.Exception.Message)"
    }
  }
}

function Read-PromptWithDefault {
  param(
    [string]$Prompt,
    [string]$Default = ""
  )
  if ($NonInteractive) {
    return $Default
  }
  $suffix = if ($Default -ne "") { " [$Default]" } else { "" }
  $answer = Read-Host "$Prompt$suffix"
  if ([string]::IsNullOrWhiteSpace($answer)) {
    return $Default
  }
  return $answer.Trim()
}

function Read-YesNo {
  param(
    [string]$Prompt,
    [bool]$DefaultYes = $true
  )
  if ($NonInteractive) {
    return $DefaultYes
  }
  $hint = if ($DefaultYes) { "[S/n]" } else { "[s/N]" }
  $answer = Read-Host "$Prompt $hint"
  if ([string]::IsNullOrWhiteSpace($answer)) {
    return $DefaultYes
  }
  $norm = $answer.Trim().ToLowerInvariant()
  return ($norm -eq "s" -or $norm -eq "si" -or $norm -eq "y" -or $norm -eq "yes")
}

function Ensure-SpoolerRunning {
  try {
    $spooler = Get-Service -Name "Spooler" -ErrorAction Stop
    if ($spooler.Status -ne "Running") {
      Write-Log -Level "WARN" -Message "El servicio Spooler (Cola de impresion) estaba $($spooler.Status). Intentando iniciar..."
      Start-Service -Name "Spooler" -ErrorAction Stop
      $spooler.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Running, [TimeSpan]::FromSeconds(15))
      Write-Log -Level "OK" -Message "Servicio Spooler iniciado correctamente."
    }
    return $true
  } catch {
    Write-Log -Level "WARN" -Message "No se pudo verificar/iniciar el Spooler de Windows: $($_.Exception.Message)"
    return $false
  }
}

function Test-PortBlocked {
  param([int]$Port)
  try {
    if (Get-Command Get-NetTCPConnection -ErrorAction SilentlyContinue) {
      $conn = Get-NetTCPConnection -LocalPort $Port -State Listen -ErrorAction SilentlyContinue | Select-Object -First 1
      if ($conn) {
        $proc = Get-Process -Id $conn.OwningProcess -ErrorAction SilentlyContinue
        $procName = if ($proc) { $proc.ProcessName } else { "PID $($conn.OwningProcess)" }
        return "$procName (PID $($conn.OwningProcess))"
      }
    }
  } catch {}
  return $null
}

function Test-IsLikelyThermalPrinter {
  param($Printer)
  $text = "$($Printer.Name) $($Printer.DriverName)".ToLowerInvariant()
  foreach ($kw in @("pos", "tm-", "epson", "xp-", "xprinter", "elgin", "bematech", "thermal", "termica", "ticke", "80mm", "58mm", "generic / text", "bixolon", "star ")) {
    if ($text.Contains($kw)) {
      return $true
    }
  }
  return $false
}

function Get-WindowsPrintersList {
  $list = @()
  try {
    if (Get-Command Get-Printer -ErrorAction SilentlyContinue) {
      $list = Get-Printer | Select-Object Name, DriverName, PortName, PrinterStatus
    } else {
      $list = Get-CimInstance -ClassName Win32_Printer | Select-Object Name, DriverName, PortName, PrinterStatus
    }
  } catch {
    Write-Log -Level "WARN" -Message "No se pudieron enumerar las impresoras de Windows: $($_.Exception.Message)"
  }
  return @($list)
}

function Show-GuiPrinterWizard {
  param(
    [Parameter(Mandatory = $true)]$CfgJson,
    [Parameter(Mandatory = $true)][string]$ConfigPath,
    [Parameter(Mandatory = $true)][int]$ListenPort,
    [Parameter(Mandatory = $true)][string]$ServiceName
  )

  Add-Type -AssemblyName System.Windows.Forms
  Add-Type -AssemblyName System.Drawing
  [System.Windows.Forms.Application]::EnableVisualStyles()

  [void](Ensure-SpoolerRunning)
  $winPrinters = Get-WindowsPrintersList

  $form = New-Object System.Windows.Forms.Form
  $form.Text = "TomaPedidos Print Agent - Configuracion"
  $form.Size = New-Object System.Drawing.Size(540, 500)
  $form.StartPosition = "CenterScreen"
  $form.FormBorderStyle = "FixedDialog"
  $form.MaximizeBox = $false
  $form.BackColor = [System.Drawing.Color]::White
  $form.Font = New-Object System.Drawing.Font("Segoe UI", 9.5)
  $form.TopMost = $true
  $form.Add_Shown({
    $form.Activate()
    $form.TopMost = $false
  })

  $lblTitle = New-Object System.Windows.Forms.Label
  $lblTitle.Text = "Configura tu impresora en 2 pasos"
  $lblTitle.Font = New-Object System.Drawing.Font("Segoe UI", 13, [System.Drawing.FontStyle]::Bold)
  $lblTitle.ForeColor = [System.Drawing.Color]::FromArgb(154, 52, 18)
  $lblTitle.Location = New-Object System.Drawing.Point(24, 18)
  $lblTitle.Size = New-Object System.Drawing.Size(480, 28)
  $form.Controls.Add($lblTitle)

  $lblSub = New-Object System.Windows.Forms.Label
  $lblSub.Text = "Local: $($CfgJson.tenant.id) / $($CfgJson.tenant.branch_id)  |  Servicio activo en puerto $ListenPort"
  $lblSub.ForeColor = [System.Drawing.Color]::FromArgb(100, 116, 139)
  $lblSub.Location = New-Object System.Drawing.Point(24, 48)
  $lblSub.Size = New-Object System.Drawing.Size(480, 20)
  $form.Controls.Add($lblSub)

  $lblPrinter = New-Object System.Windows.Forms.Label
  $lblPrinter.Text = "1. Elegi la impresora conectada a esta PC:"
  $lblPrinter.Font = New-Object System.Drawing.Font("Segoe UI", 9.5, [System.Drawing.FontStyle]::Bold)
  $lblPrinter.Location = New-Object System.Drawing.Point(24, 84)
  $lblPrinter.Size = New-Object System.Drawing.Size(480, 22)
  $form.Controls.Add($lblPrinter)

  $cmbPrinter = New-Object System.Windows.Forms.ComboBox
  $cmbPrinter.DropDownStyle = "DropDownList"
  $cmbPrinter.Location = New-Object System.Drawing.Point(24, 108)
  $cmbPrinter.Size = New-Object System.Drawing.Size(474, 28)

  $printerChoices = [System.Collections.ArrayList]::new()
  $defaultSelectIdx = 0
  $idx = 0
  foreach ($wp in $winPrinters) {
    $isThermal = Test-IsLikelyThermalPrinter -Printer $wp
    $label = if ($isThermal) { "$($wp.Name)  [TERMICA RECOMENDADA]" } else { "$($wp.Name)  (Impresora comun)" }
    [void]$cmbPrinter.Items.Add($label)
    [void]$printerChoices.Add([PSCustomObject]@{ Kind = "win"; Printer = $wp; IsThermal = $isThermal })
    if ($isThermal -and $defaultSelectIdx -eq 0) { $defaultSelectIdx = $idx }
    $idx++
  }
  [void]$cmbPrinter.Items.Add("Impresora de Red (Cable / Wi-Fi por IP)")
  [void]$printerChoices.Add([PSCustomObject]@{ Kind = "network"; Printer = $null; IsThermal = $true })
  if ($cmbPrinter.Items.Count -gt 0) { $cmbPrinter.SelectedIndex = $defaultSelectIdx }
  $form.Controls.Add($cmbPrinter)

  $lblIp = New-Object System.Windows.Forms.Label
  $lblIp.Text = "IP de la impresora:"
  $lblIp.Location = New-Object System.Drawing.Point(24, 145)
  $lblIp.Size = New-Object System.Drawing.Size(130, 22)
  $lblIp.Visible = $false
  $form.Controls.Add($lblIp)

  $txtIp = New-Object System.Windows.Forms.TextBox
  $txtIp.Text = "192.168.1.100"
  $txtIp.Location = New-Object System.Drawing.Point(155, 143)
  $txtIp.Size = New-Object System.Drawing.Size(160, 24)
  $txtIp.Visible = $false
  $form.Controls.Add($txtIp)

  $lblNetPort = New-Object System.Windows.Forms.Label
  $lblNetPort.Text = "Puerto:"
  $lblNetPort.Location = New-Object System.Drawing.Point(330, 145)
  $lblNetPort.Size = New-Object System.Drawing.Size(55, 22)
  $lblNetPort.Visible = $false
  $form.Controls.Add($lblNetPort)

  $txtNetPort = New-Object System.Windows.Forms.TextBox
  $txtNetPort.Text = "9100"
  $txtNetPort.Location = New-Object System.Drawing.Point(390, 143)
  $txtNetPort.Size = New-Object System.Drawing.Size(108, 24)
  $txtNetPort.Visible = $false
  $form.Controls.Add($txtNetPort)

  $cmbPrinter.Add_SelectedIndexChanged({
    $sel = $printerChoices[$cmbPrinter.SelectedIndex]
    $isNet = ($sel -and $sel.Kind -eq "network")
    $lblIp.Visible = $isNet; $txtIp.Visible = $isNet
    $lblNetPort.Visible = $isNet; $txtNetPort.Visible = $isNet
  })

  $lblRole = New-Object System.Windows.Forms.Label
  $lblRole.Text = "¿Para que se usa?"
  $lblRole.Location = New-Object System.Drawing.Point(24, 182)
  $lblRole.Size = New-Object System.Drawing.Size(220, 20)
  $form.Controls.Add($lblRole)

  $cmbRole = New-Object System.Windows.Forms.ComboBox
  $cmbRole.DropDownStyle = "DropDownList"
  $cmbRole.Location = New-Object System.Drawing.Point(24, 204)
  $cmbRole.Size = New-Object System.Drawing.Size(225, 28)
  [void]$cmbRole.Items.Add("Cocina (Comandas)")
  [void]$cmbRole.Items.Add("Caja (Tickets / Cuenta)")
  [void]$cmbRole.Items.Add("Barra / Pizzeria")
  $cmbRole.SelectedIndex = 0
  $form.Controls.Add($cmbRole)

  $lblWidth = New-Object System.Windows.Forms.Label
  $lblWidth.Text = "Ancho del rollo de papel:"
  $lblWidth.Location = New-Object System.Drawing.Point(273, 182)
  $lblWidth.Size = New-Object System.Drawing.Size(225, 20)
  $form.Controls.Add($lblWidth)

  $cmbWidth = New-Object System.Windows.Forms.ComboBox
  $cmbWidth.DropDownStyle = "DropDownList"
  $cmbWidth.Location = New-Object System.Drawing.Point(273, 204)
  $cmbWidth.Size = New-Object System.Drawing.Size(225, 28)
  [void]$cmbWidth.Items.Add("80 mm (Normal - mas comun)")
  [void]$cmbWidth.Items.Add("58 mm (Rollo chico)")
  $cmbWidth.SelectedIndex = 0
  $form.Controls.Add($cmbWidth)

  $lblStatus = New-Object System.Windows.Forms.Label
  $lblStatus.Text = "2. Toca el boton naranja para guardar la impresora y enviar un ticket de prueba."
  $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(71, 85, 105)
  $lblStatus.Location = New-Object System.Drawing.Point(24, 252)
  $lblStatus.Size = New-Object System.Drawing.Size(474, 52)
  $form.Controls.Add($lblStatus)

  $btnTest = New-Object System.Windows.Forms.Button
  $btnTest.Text = "Guardar e imprimir ticket de prueba"
  $btnTest.BackColor = [System.Drawing.Color]::FromArgb(249, 115, 22)
  $btnTest.ForeColor = [System.Drawing.Color]::White
  $btnTest.FlatStyle = "Flat"
  $btnTest.Font = New-Object System.Drawing.Font("Segoe UI", 10, [System.Drawing.FontStyle]::Bold)
  $btnTest.Location = New-Object System.Drawing.Point(24, 312)
  $btnTest.Size = New-Object System.Drawing.Size(474, 42)
  $form.Controls.Add($btnTest)

  $btnOk = New-Object System.Windows.Forms.Button
  $btnOk.Text = "¡Si, salio perfecto! (Finalizar)"
  $btnOk.BackColor = [System.Drawing.Color]::FromArgb(22, 163, 74)
  $btnOk.ForeColor = [System.Drawing.Color]::White
  $btnOk.FlatStyle = "Flat"
  $btnOk.Font = New-Object System.Drawing.Font("Segoe UI", 9.5, [System.Drawing.FontStyle]::Bold)
  $btnOk.Location = New-Object System.Drawing.Point(24, 370)
  $btnOk.Size = New-Object System.Drawing.Size(230, 40)
  $btnOk.Enabled = $false
  $form.Controls.Add($btnOk)

  $btnRetry = New-Object System.Windows.Forms.Button
  $btnRetry.Text = "No salio, probar otro modo"
  $btnRetry.BackColor = [System.Drawing.Color]::FromArgb(241, 245, 249)
  $btnRetry.ForeColor = [System.Drawing.Color]::FromArgb(30, 41, 59)
  $btnRetry.FlatStyle = "Flat"
  $btnRetry.Location = New-Object System.Drawing.Point(268, 370)
  $btnRetry.Size = New-Object System.Drawing.Size(230, 40)
  $btnRetry.Enabled = $false
  $form.Controls.Add($btnRetry)

  $script:GuiPrinterId = "cocina"
  $script:GuiPrinterType = "usb"

  $sendTestAction = {
    param([bool]$ToggleMode = $false)
    try {
      $sel = $printerChoices[$cmbPrinter.SelectedIndex]
      $roleId = switch ($cmbRole.SelectedIndex) { 1 { "caja" } 2 { "barra" } default { "cocina" } }
      $chars = if ($cmbWidth.SelectedIndex -eq 1) { 32 } else { 42 }

      if ($ToggleMode) {
        if ($script:GuiPrinterType -eq "network") {
          $lblStatus.Text = "Para impresoras de red, verifica que este encendida y que la IP $($txtIp.Text) sea correcta."
          $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(180, 83, 9)
          return
        }
        $script:GuiPrinterType = if ($script:GuiPrinterType -eq "usb") { "usb-gdi" } else { "usb" }
      } else {
        if ($sel.Kind -eq "network") {
          $script:GuiPrinterType = "network"
        } else {
          $script:GuiPrinterType = if ($sel.IsThermal) { "usb" } else { "usb-gdi" }
        }
      }
      $script:GuiPrinterId = $roleId

      if ($script:GuiPrinterType -eq "network") {
        $ipVal = $txtIp.Text.Trim()
        $np = 0
        if ([string]::IsNullOrWhiteSpace($ipVal) -or -not [int]::TryParse($txtNetPort.Text, [ref]$np) -or $np -lt 1 -or $np -gt 65535) {
          $lblStatus.Text = "Ingresa una IP valida y un puerto entre 1 y 65535."
          $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(220, 38, 38)
          return
        }
        $lblStatus.Text = "Probando conexion a ${ipVal}:${np}..."
        $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(71, 85, 105)
        $form.Refresh()
        $tcp = New-Object System.Net.Sockets.TcpClient
        try {
          $iar = $tcp.BeginConnect($ipVal, $np, $null, $null)
          if (-not $iar.AsyncWaitHandle.WaitOne(1500, $false)) {
            throw "Tiempo de espera agotado"
          }
          $tcp.EndConnect($iar)
        } catch {
          $lblStatus.Text = "No se pudo conectar a ${ipVal}:${np}. Verifica que este encendida, conectada a la red y que la IP sea correcta."
          $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(220, 38, 38)
          return
        } finally {
          $tcp.Close()
        }
        $pObj = New-DefaultPrinterObject -Id $roleId -Name "Impresora $roleId" -Type "network" -HostIp $ipVal -NetPort $np -CharsPerLine $chars
      } else {
        $pObj = New-DefaultPrinterObject -Id $roleId -Name $sel.Printer.Name -Type $script:GuiPrinterType -SystemName $sel.Printer.Name -CharsPerLine $chars
      }

      $list = [System.Collections.ArrayList]::new()
      if ($CfgJson.printers) {
        foreach ($ep in $CfgJson.printers) {
          if ($ep.id -ne $roleId) { [void]$list.Add($ep) }
        }
      }
      [void]$list.Add($pObj)
      $CfgJson.printers = @($list)
      Write-Utf8NoBom -Path $ConfigPath -Content ($CfgJson | ConvertTo-Json -Depth 10)
      $script:Report.printers = @($CfgJson.printers | ForEach-Object { @{ id = $_.id; name = $_.name; type = $_.type; system_name = $_.system_name; host = $_.host } })

      $lblStatus.Text = "Aplicando configuracion y enviando ticket de prueba..."
      $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(71, 85, 105)
      $form.Refresh()

      Restart-Service -Name $ServiceName -ErrorAction Stop
      $h = Wait-ForAgentHealth -Port $ListenPort
      if ($null -eq $h) { throw "El servicio no respondio despues de reiniciar." }

      $testBody = @{
        printer_id = $roleId
        template   = "kitchen"
        header     = @{ order_number = 0; customer_name = "PRUEBA INSTALADOR"; delivery_type = "take_away" }
        items      = @( @{ qty = 1; name = "Ticket de prueba TomaPedidos" } )
      } | ConvertTo-Json -Depth 6
      $printResp = Invoke-RestMethod -Uri "http://127.0.0.1:${ListenPort}/print" -Method Post -ContentType "application/json; charset=utf-8" -Body $testBody -TimeoutSec 10
      Add-StepResult -StepId "6_prueba_impresion" -Title "Ticket de prueba enviado (GUI)" -Status "ok" -Detail "job_id=$($printResp.id) printer=$roleId type=$($script:GuiPrinterType)"

      $lblStatus.Text = "Ticket enviado (modo $($script:GuiPrinterType)). Revisa si salio el papel y confirma abajo."
      $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(22, 163, 74)
      $btnOk.Enabled = $true
      $btnRetry.Enabled = $true
    } catch {
      Add-StepResult -StepId "6_prueba_impresion" -Title "Error en prueba GUI" -Status "warn" -Detail $_.Exception.Message
      $lblStatus.Text = "Error: $($_.Exception.Message)"
      $lblStatus.ForeColor = [System.Drawing.Color]::FromArgb(220, 38, 38)
      $btnRetry.Enabled = $true
    }
  }

  $btnTest.Add_Click({ & $sendTestAction $false })
  $btnRetry.Add_Click({ & $sendTestAction $true })
  $btnOk.Add_Click({
    Add-StepResult -StepId "6_prueba_validacion" -Title "Impresion fisica confirmada en instalador GUI" -Status "ok" -Detail "printer=$($script:GuiPrinterId) type=$($script:GuiPrinterType)"
    $form.DialogResult = [System.Windows.Forms.DialogResult]::OK
    $form.Close()
  })

  $dialogRes = $form.ShowDialog()
  if ($dialogRes -ne [System.Windows.Forms.DialogResult]::OK) {
    Add-StepResult -StepId "6_prueba_validacion" -Title "Asistente cerrado sin confirmar ticket" -Status "warn" -Detail "El operador cerro la ventana antes de confirmar la prueba."
  }
}

function Wait-ForAgentHealth {
  param([int]$Port = 4510)
  for ($attempt = 0; $attempt -lt 30; $attempt++) {
    try {
      $res = Invoke-RestMethod -Uri "http://127.0.0.1:${Port}/health" -TimeoutSec 2 -ErrorAction Stop
      if ($res -and $res.ok -eq $true) {
        return $res
      }
    } catch {}
    if ($attempt -lt 29) {
      Start-Sleep -Seconds 1
    }
  }
  return $null
}

function Restore-PreviousService {
  param(
    [string]$Name,
    [string]$BinaryPath,
    [string]$InstalledBinary,
    [string]$BinaryBackup,
    [int]$Port = 4510
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
    & sc.exe config $Name binPath= $BinaryPath obj= LocalSystem password= "" | Out-Null
    if ($LASTEXITCODE -ne 0) {
      throw "sc.exe config rollback failed with exit code $LASTEXITCODE."
    }
    Start-Service -Name $Name -ErrorAction Stop
    return ($null -ne (Wait-ForAgentHealth -Port $Port))
  } catch {
    Write-Log -Level "ERROR" -Message "Error en rollback: $($_.Exception.Message)"
    return $false
  }
}

function New-DefaultPrinterObject {
  param(
    [string]$Id,
    [string]$Name,
    [string]$Type,
    [string]$SystemName = "",
    [string]$HostIp = "",
    [int]$NetPort = 9100,
    [int]$CharsPerLine = 42
  )
  $obj = [ordered]@{
    id                 = $Id
    name               = $Name
    type               = $Type
    code_page          = "cp850"
    chars_per_line     = $CharsPerLine
    cut                = "partial"
    item_double_height = $false
  }
  if ($Type -eq "network") {
    $obj["host"] = $HostIp
    $obj["port"] = $NetPort
    $obj["timeout_ms"] = 3000
  } else {
    $obj["system_name"] = $SystemName
  }
  return [PSCustomObject]$obj
}

# ==============================================================================
# INICIO DEL WIZARD
# ==============================================================================
Write-Log -Level "INFO" -Message "=== Inicio de instalacion TomaPedidos Print Agent ($arch) ==="

$backupDir = $null
$oldBinPath = $null
$installedBinaryBackup = $null
$hadService = $false
$listenPort = 4510

try {
  # ----------------------------------------------------------------------------
  # PASO 1: PERMISOS Y ENTORNO
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "1_permisos"
  Write-Host ""
  Write-Host ">>> PASO 1/6: Verificacion de permisos y carpetas" -ForegroundColor Cyan

  $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
  $principal = New-Object Security.Principal.WindowsPrincipal($identity)
  $isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
  $script:Report.is_admin = $isAdmin

  if (-not $isAdmin) {
    throw "Se requieren permisos de Administrador. Ejecuta 'instalar.bat' o abre PowerShell como Administrador."
  }

  foreach ($dir in @($binDir, $configDir, $dataDir, $logsDir)) {
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
  }
  Add-StepResult -StepId "1_permisos" -Title "Permisos de Administrador y carpetas" -Status "ok" -Detail "Usuario $($script:Report.windows_user) con permisos Admin."

  # ----------------------------------------------------------------------------
  # PASO 2: BINARIO Y RESPALDO
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "2_binario"
  Write-Host ""
  Write-Host ">>> PASO 2/6: Obtencion del binario y respaldo" -ForegroundColor Cyan

  $scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
  $candidateLocal = @(
    $LocalBinary,
    (Join-Path $scriptDir $releaseAssetName),
    (Join-Path $scriptDir "print-agent.exe"),
    (Join-Path (Split-Path -Parent $scriptDir) "print-agent.exe")
  ) | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1

  $useLocal = $false
  $tempFile = "$binDir\print-agent.download.exe"
  $checksumsFile = "$binDir\SHA256SUMS.download"

  if ($candidateLocal) {
    Write-Host "Se encontro un binario local en: $candidateLocal"
    if ($LocalBinary -ne "" -or (Read-YesNo -Prompt "Usar este binario local en lugar de descargar de GitHub?" -DefaultYes $true)) {
      $useLocal = $true
      Copy-Item -Force -Path $candidateLocal -Destination $tempFile
      Add-StepResult -StepId "2_binario_origen" -Title "Binario local seleccionado" -Status "ok" -Detail $candidateLocal
    }
  }

  if (-not $useLocal) {
    $Version = Read-PromptWithDefault -Prompt "Version a descargar de GitHub (ej. v0.4.0 o 'latest')" -Default $Version
    $script:Report.target_version = $Version
    $releaseBase = if ($Version -eq "latest") {
      "https://github.com/${repo}/releases/latest/download"
    } else {
      "https://github.com/${repo}/releases/download/${Version}"
    }
    $url = "${releaseBase}/${releaseAssetName}"
    $checksumsUrl = "${releaseBase}/SHA256SUMS"

    Write-Log -Level "INFO" -Message "Descargando $url ..."
    try {
      Invoke-WebRequest -Uri $url -OutFile $tempFile -UseBasicParsing
      Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsFile -UseBasicParsing
      $checksumEntry = Get-Content -Path $checksumsFile | ForEach-Object {
        if ($_ -match '^\s*([A-Fa-f0-9]{64})\s+\*?(.+?)\s*$') {
          [PSCustomObject]@{ Hash = $Matches[1]; Name = $Matches[2] }
        }
      } | Where-Object { $_.Name -eq $releaseAssetName } | Select-Object -First 1
      if (-not $checksumEntry) {
        throw "SHA256SUMS no contiene entrada para $releaseAssetName."
      }
      $actualHash = (Get-FileHash -Algorithm SHA256 -Path $tempFile).Hash
      if ($actualHash -ine $checksumEntry.Hash) {
        throw "Fallo verificacion SHA256 (esperado $($checksumEntry.Hash), obtenido $actualHash)."
      }
      Remove-Item -Force -ErrorAction SilentlyContinue $checksumsFile
      Add-StepResult -StepId "2_binario_origen" -Title "Descarga y verificacion SHA256" -Status "ok" -Detail "$releaseAssetName ($Version)"
    } catch {
      Remove-Item -Force -ErrorAction SilentlyContinue $tempFile, $checksumsFile
      throw "No se pudo descargar/verificar el binario desde GitHub: $($_.Exception.Message)"
    }
  }

  $service = Get-Service -Name $serviceName -ErrorAction SilentlyContinue
  $hadService = $null -ne $service
  $legacyJobsPattern = "$legacyConfigDir\jobs.db*"
  $hasLegacyFiles = (Test-Path $legacyDest) -or (Test-Path $legacyConfig) -or (Test-Path -Path $legacyJobsPattern)
  $isMigration = $hadService -or $hasLegacyFiles

  if ($hadService -and $service.Status -ne "Stopped") {
    Write-Log -Level "INFO" -Message "Deteniendo servicio existente $serviceName..."
    Stop-Service -Name $serviceName -ErrorAction Stop
    $service = Get-Service -Name $serviceName -ErrorAction Stop
    $service.WaitForStatus([System.ServiceProcess.ServiceControllerStatus]::Stopped, [TimeSpan]::FromSeconds(60))
  }

  if ($isMigration) {
    $backupDir = "$dataRoot\backups\migration-$timestampTag"
    New-Item -ItemType Directory -Force -Path $backupDir | Out-Null
    if (Test-Path $configPath) { Copy-Item -Path $configPath -Destination "$backupDir\printers.current.json" -Force }
    if (Test-Path $legacyConfig) { Copy-Item -Path $legacyConfig -Destination "$backupDir\printers.legacy.json" -Force }
    if (Test-Path -Path $legacyJobsPattern) { Copy-Item -Path $legacyJobsPattern -Destination $backupDir -Force }

    if ($hadService) {
      & sc.exe qc $serviceName | Out-File -FilePath "$backupDir\service-before.txt"
      $existingSvc = Get-CimInstance -ClassName Win32_Service -Filter "Name='$serviceName'"
      if ($null -eq $existingSvc -or [string]::IsNullOrWhiteSpace($existingSvc.PathName)) {
        throw "No se pudo leer binPath previo del servicio $serviceName. Backup: $backupDir"
      }
      $oldBinPath = $existingSvc.PathName
      if (Test-Path $dest) {
        $installedBinaryBackup = "$backupDir\program-files-before.exe"
        Copy-Item -Force -Path $dest -Destination $installedBinaryBackup
      }
    }
    Add-StepResult -StepId "2_respaldo" -Title "Respaldo previo a actualizacion" -Status "ok" -Detail $backupDir
  }

  Copy-Item -Force -Path $tempFile -Destination $dest
  Remove-Item -Force -ErrorAction SilentlyContinue $tempFile
  if ($isMigration -and (Test-Path -Path $legacyJobsPattern)) {
    Copy-Item -Path $legacyJobsPattern -Destination $dataDir -Force
  }

  try {
    $verOutput = (& "$dest" version 2>&1) -join " "
    $script:Report.installed_version = $verOutput.Trim()
  } catch {}
  Add-StepResult -StepId "2_binario" -Title "Binario instalado en Program Files" -Status "ok" -Detail "$dest ($($script:Report.installed_version))"

  # ----------------------------------------------------------------------------
  # PASO 3: CONFIGURACION DEL LOCAL
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "3_configuracion"
  Write-Host ""
  Write-Host ">>> PASO 3/6: Configuracion del local" -ForegroundColor Cyan

  if ((Test-Path $legacyConfig) -and -not (Test-Path $configPath)) {
    Copy-Item -Path $legacyConfig -Destination $configPath -Force
  }
  if (-not (Test-Path $configPath)) {
    & "$dest" init-config --config "$configPath" | Out-Null
    if ($LASTEXITCODE -ne 0) {
      throw "print-agent init-config fallo con codigo $LASTEXITCODE"
    }
  }

  $cfgJson = $null
  if (Test-Path $configPath) {
    try {
      $cfgJson = Get-Content -Raw -Path $configPath -Encoding UTF8 | ConvertFrom-Json
      if ($null -eq $cfgJson -or $cfgJson -isnot [PSCustomObject]) {
        throw "El JSON raiz no es un objeto."
      }
    } catch {
      $corruptBackup = "$dataRoot\backups\printers.corrupt-$timestampTag.json"
      New-Item -ItemType Directory -Force -Path "$dataRoot\backups" | Out-Null
      Copy-Item -Path $configPath -Destination $corruptBackup -Force -ErrorAction SilentlyContinue
      Remove-Item -Path $configPath -Force -ErrorAction SilentlyContinue
      Add-StepResult -StepId "3_config_recuperada" -Title "Configuracion corrupta respaldada y regenerada" -Status "warn" -Detail "Backup: $corruptBackup ($($_.Exception.Message))"
      $cfgJson = $null
    }
  }
  if ($null -eq $cfgJson) {
    & "$dest" init-config --config "$configPath" | Out-Null
    if ($LASTEXITCODE -ne 0) {
      throw "print-agent init-config fallo con codigo $LASTEXITCODE"
    }
    $cfgJson = Get-Content -Raw -Path $configPath -Encoding UTF8 | ConvertFrom-Json
  }

  $curTenant = if ($cfgJson.tenant -and $cfgJson.tenant.id) { $cfgJson.tenant.id } else { "tenant_default" }
  $curBranch = if ($cfgJson.tenant -and $cfgJson.tenant.branch_id) { $cfgJson.tenant.branch_id } else { "branch_default" }
  $curPort   = if ($cfgJson.port) { [string]$cfgJson.port } else { "4510" }
  $curPin    = if ($cfgJson.panel -and $cfgJson.panel.pin) { $cfgJson.panel.pin } else { "1234" }
  $curReport = if ($cfgJson.PSObject.Properties["report_url"] -and $cfgJson.report_url) { $cfgJson.report_url } else { "" }

  # Buscar tomapedidos-setup*.json descargado desde TomaPedidos (incluye duplicados tipo "(1)").
  $setupJsonFile = @(
    (Get-ChildItem -Path $scriptDir -Filter "tomapedidos-setup*.json" -File -ErrorAction SilentlyContinue),
    (Get-ChildItem -Path "$env:USERPROFILE\Downloads" -Filter "tomapedidos-setup*.json" -File -ErrorAction SilentlyContinue),
    (Get-ChildItem -Path $configDir -Filter "tomapedidos-setup*.json" -File -ErrorAction SilentlyContinue)
  ) | Where-Object { $_ } | Sort-Object LastWriteTime -Descending | Select-Object -First 1 -ExpandProperty FullName

  $preloadedSetup = $null
  if ($setupJsonFile) {
    try {
      $preloadedSetup = Get-Content -Raw -Path $setupJsonFile -Encoding UTF8 | ConvertFrom-Json
      Write-Log -Level "OK" -Message "Configuracion pre-descargada detectada: $setupJsonFile"
    } catch {
      Write-Log -Level "WARN" -Message "No se pudo leer $setupJsonFile`: $($_.Exception.Message)"
    }
  }

  if ($preloadedSetup) {
    $newTenant = if ($preloadedSetup.tenant_id) { [string]$preloadedSetup.tenant_id } else { $curTenant }
    $newBranch = if ($preloadedSetup.branch_id) { [string]$preloadedSetup.branch_id } else { $curBranch }
    $listenPort = if ($preloadedSetup.port) { [int]$preloadedSetup.port } else { [int]$curPort }
    $newPin = if ($preloadedSetup.pin) { [string]$preloadedSetup.pin } else { $curPin }
    if ([string]::IsNullOrWhiteSpace($ReportUrl) -and $preloadedSetup.report_url) {
      $ReportUrl = [string]$preloadedSetup.report_url
    }
  } elseif ($GuiSetup -or $WebSetup -or $NonInteractive) {
    $newTenant = $curTenant
    $newBranch = $curBranch
    $listenPort = [int]$curPort
    $newPin = $curPin
    if ([string]::IsNullOrWhiteSpace($ReportUrl)) { $ReportUrl = $curReport }
  } else {
    $newTenant = Read-PromptWithDefault -Prompt "Tenant ID (identificador del comercio)" -Default $curTenant
    $newBranch = Read-PromptWithDefault -Prompt "Branch ID (identificador de la sucursal)" -Default $curBranch
    $newPortStr = Read-PromptWithDefault -Prompt "Puerto HTTP local" -Default $curPort
    $parsedPort = 4510
    if (-not [int]::TryParse($newPortStr, [ref]$parsedPort) -or $parsedPort -lt 1 -or $parsedPort -gt 65535) {
      throw "Puerto invalido: '$newPortStr'. Debe ser un numero entre 1 y 65535."
    }
    $listenPort = $parsedPort
    $newPin = Read-PromptWithDefault -Prompt "PIN del panel web local" -Default $curPin

    if ([string]::IsNullOrWhiteSpace($ReportUrl)) {
      $ReportUrl = Read-PromptWithDefault -Prompt "URL del servidor para enviar reporte (opcional, Enter para omitir)" -Default $curReport
    }
  }

  $cfgJson.port = $listenPort
  $cfgJson.bind = "127.0.0.1"
  if ($cfgJson.PSObject.Properties["log_file"]) {
    $cfgJson.log_file = $agentLogPath
  } else {
    $cfgJson | Add-Member -MemberType NoteProperty -Name "log_file" -Value $agentLogPath
  }
  if (-not [string]::IsNullOrWhiteSpace($ReportUrl)) {
    if ($cfgJson.PSObject.Properties["report_url"]) {
      $cfgJson.report_url = $ReportUrl
    } else {
      $cfgJson | Add-Member -MemberType NoteProperty -Name "report_url" -Value $ReportUrl
    }
  }
  if ($null -eq $cfgJson.tenant) {
    $cfgJson | Add-Member -MemberType NoteProperty -Name "tenant" -Value ([PSCustomObject]@{ id = $newTenant; branch_id = $newBranch })
  } else {
    $cfgJson.tenant.id = $newTenant
    $cfgJson.tenant.branch_id = $newBranch
  }
  if ($null -eq $cfgJson.panel) {
    $cfgJson | Add-Member -MemberType NoteProperty -Name "panel" -Value ([PSCustomObject]@{ enabled = $true; pin = $newPin })
  } else {
    $cfgJson.panel.enabled = $true
    $cfgJson.panel.pin = $newPin
  }
  if ($null -eq $cfgJson.queue) {
    $cfgJson | Add-Member -MemberType NoteProperty -Name "queue" -Value ([PSCustomObject]@{ persist_path = $jobsPath })
  } elseif ($cfgJson.queue.PSObject.Properties["persist_path"]) {
    $cfgJson.queue.persist_path = $jobsPath
  } else {
    $cfgJson.queue | Add-Member -MemberType NoteProperty -Name "persist_path" -Value $jobsPath
  }

  $script:Report.tenant_id = $newTenant
  $script:Report.branch_id = $newBranch
  $script:Report.port = $listenPort
  Add-StepResult -StepId "3_configuracion" -Title "Configuracion del local" -Status "ok" -Detail "tenant=$newTenant branch=$newBranch port=$listenPort"

  # ----------------------------------------------------------------------------
  # PASO 4: DETECCION Y ASIGNACION DE IMPRESORAS
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "4_impresoras"
  Write-Host ""
  Write-Host ">>> PASO 4/6: Deteccion y asignacion de impresoras" -ForegroundColor Cyan

  $existingPrinters = @()
  if ($cfgJson.printers) {
    $existingPrinters = @($cfgJson.printers)
  }

  $reconfigurePrinters = -not ($GuiSetup -or $WebSetup -or $NonInteractive)
  if ($existingPrinters.Count -gt 0) {
    Write-Host "Impresoras actualmente configuradas:"
    foreach ($ep in $existingPrinters) {
      $target = if ($ep.type -eq "network") { "$($ep.host):$($ep.port)" } else { "$($ep.system_name) $($ep.file_path)" }
      Write-Host ("  - {0} ({1}) [{2}] -> {3}" -f $ep.id, $ep.name, $ep.type, $target)
    }
    if ($GuiSetup -or $WebSetup -or $NonInteractive -or -not (Read-YesNo -Prompt "Queres reconfigurar o agregar impresoras ahora?" -DefaultYes $false)) {
      $reconfigurePrinters = $false
    }
  }

  if ($reconfigurePrinters) {
    [void](Ensure-SpoolerRunning)
    $winPrinters = Get-WindowsPrintersList
    $newPrinters = [System.Collections.ArrayList]::new()

    if ($existingPrinters.Count -gt 0 -and (Read-YesNo -Prompt "Mantener las impresoras existentes y solo agregar nuevas?" -DefaultYes $true)) {
      foreach ($ep in $existingPrinters) { [void]$newPrinters.Add($ep) }
    }

    do {
      Write-Host ""
      if ($winPrinters.Count -gt 0) {
        Write-Host "Impresoras detectadas en Windows:" -ForegroundColor White
        for ($i = 0; $i -lt $winPrinters.Count; $i++) {
          $wp = $winPrinters[$i]
          $thermalTag = if (Test-IsLikelyThermalPrinter -Printer $wp) { " [TERMICA]" } else { "" }
          $statusTag = if ($wp.PrinterStatus -and $wp.PrinterStatus -ne "Normal" -and $wp.PrinterStatus -ne 0) { " [Estado: $($wp.PrinterStatus)]" } else { "" }
          Write-Host ("  [{0}] {1}{2}{3}  (Driver: {4} | Puerto: {5})" -f ($i + 1), $wp.Name, $thermalTag, $statusTag, $wp.DriverName, $wp.PortName)
        }
      } else {
        Write-Host "No se detectaron impresoras instaladas en el spooler de Windows." -ForegroundColor Yellow
      }
      Write-Host "  [R] Configurar impresora de Red (TCP/IP puerto 9100)"
      if ($newPrinters.Count -gt 0) {
        Write-Host "  [F] Finalizar seleccion"
      }

      $choice = Read-PromptWithDefault -Prompt "Elegi el numero de impresora o [R]" -Default $(if ($winPrinters.Count -gt 0) { "1" } else { "R" })
      $choiceUpper = $choice.ToUpperInvariant()

      if ($choiceUpper -eq "F" -and $newPrinters.Count -gt 0) {
        break
      }

      if ($choiceUpper -eq "R") {
        $pId = Read-PromptWithDefault -Prompt "ID logico (ej. cocina, caja, barra)" -Default "cocina"
        $pName = Read-PromptWithDefault -Prompt "Nombre visible" -Default "Impresora $pId"
        $pHost = Read-PromptWithDefault -Prompt "Direccion IP de la impresora" -Default "192.168.1.100"
        $pPortStr = Read-PromptWithDefault -Prompt "Puerto TCP" -Default "9100"
        $pPort = 9100
        [void][int]::TryParse($pPortStr, [ref]$pPort)
        $pCharsStr = Read-PromptWithDefault -Prompt "Caracteres por linea (42=80mm, 32=58mm)" -Default "42"
        $pChars = 42
        [void][int]::TryParse($pCharsStr, [ref]$pChars)
        [void]$newPrinters.Add((New-DefaultPrinterObject -Id $pId -Name $pName -Type "network" -HostIp $pHost -NetPort $pPort -CharsPerLine $pChars))
      } elseif ($choice -match '^\d+$') {
        $idx = [int]$choice - 1
        if ($idx -lt 0 -or $idx -ge $winPrinters.Count) {
          Write-Host "Numero fuera de rango." -ForegroundColor Yellow
          continue
        }
        $selectedWin = $winPrinters[$idx]
        Write-Host "Seleccionada: $($selectedWin.Name)" -ForegroundColor Green
        $pId = Read-PromptWithDefault -Prompt "ID logico para esta impresora (ej. cocina, caja, barra)" -Default $(if ($newPrinters.Count -eq 0) { "cocina" } else { "caja" })
        $pName = Read-PromptWithDefault -Prompt "Nombre visible" -Default $selectedWin.Name
        Write-Host "Tipo de impresion:"
        Write-Host "  1) usb        (Termica ESC/POS por spooler Windows - recomendada para comandas)"
        Write-Host "  2) usb-gdi    (Impresora GDI / PDF / chorro de tinta)"
        Write-Host "  3) usb-office (Oficina texto plano)"
        $typeChoice = Read-PromptWithDefault -Prompt "Elegi tipo (1/2/3)" -Default "1"
        $pType = switch ($typeChoice) {
          "2" { "usb-gdi" }
          "3" { "usb-office" }
          default { "usb" }
        }
        $pCharsStr = Read-PromptWithDefault -Prompt "Caracteres por linea (42=80mm, 32=58mm)" -Default "42"
        $pChars = 42
        [void][int]::TryParse($pCharsStr, [ref]$pChars)
        [void]$newPrinters.Add((New-DefaultPrinterObject -Id $pId -Name $pName -Type $pType -SystemName $selectedWin.Name -CharsPerLine $pChars))
      } else {
        Write-Host "Opcion invalida." -ForegroundColor Yellow
        continue
      }

      $addMore = Read-YesNo -Prompt "Queres agregar otra impresora?" -DefaultYes $false
    } while ($addMore)

    if ($newPrinters.Count -eq 0) {
      throw "No se configuro ninguna impresora. Debe haber al menos una impresora asignada."
    }
    $cfgJson.printers = @($newPrinters)
  }

  $configOut = $cfgJson | ConvertTo-Json -Depth 10
  Write-Utf8NoBom -Path $configPath -Content $configOut
  $script:Report.printers = @($cfgJson.printers | ForEach-Object { @{ id = $_.id; name = $_.name; type = $_.type; system_name = $_.system_name; host = $_.host } })
  Add-StepResult -StepId "4_impresoras" -Title "Impresoras configuradas" -Status "ok" -Detail ("Total: " + $cfgJson.printers.Count)

  # ----------------------------------------------------------------------------
  # PASO 5: REGISTRO DE SERVICIO Y PERMISOS ACL
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "5_servicio_y_acl"
  Write-Host ""
  Write-Host ">>> PASO 5/6: Registro del servicio Windows y permisos ACL" -ForegroundColor Cyan

  & "$dest" install --config "$configPath"
  $installExit = $LASTEXITCODE
  if ($installExit -ne 0) {
    if (-not $hadService) {
      throw "print-agent install fallo con codigo $installExit."
    }
    Write-Log -Level "WARN" -Message "print-agent install devolvio $installExit; intentando sc.exe config fallback..."
    $newBinPath = "`"$dest`" start --config `"$configPath`""
    $accountArg = "`"$serviceAccount`""
    & sc.exe config $serviceName binPath= $newBinPath start= auto obj= $accountArg password= ""
    if ($LASTEXITCODE -ne 0) {
      throw "sc.exe config fallback fallo con codigo $LASTEXITCODE."
    }
  }

  $serviceGrant = "${serviceAccount}:(OI)(CI)F"
  foreach ($directory in @($configDir, $dataDir, $logsDir)) {
    & icacls.exe $directory /grant $serviceGrant "Users:(OI)(CI)M" | Out-Null
    if ($LASTEXITCODE -ne 0) {
      throw "No se pudieron aplicar permisos ACL en $directory (icacls codigo $LASTEXITCODE)."
    }
  }
  # Configurar auto-reinicio ante caidas inesperadas (5s, 10s, 30s).
  & sc.exe failure $serviceName reset= 86400 actions= restart/5000/restart/10000/restart/30000 | Out-Null
  Add-StepResult -StepId "5_servicio_y_acl" -Title "Servicio registrado, auto-restart y permisos aplicados" -Status "ok" -Detail $serviceAccount

  # ----------------------------------------------------------------------------
  # PASO 6: ARRANQUE, VERIFICACION DE SALUD Y PRUEBA OPCIONAL
  # ----------------------------------------------------------------------------
  $script:CurrentStep = "6_arranque_y_salud"
  Write-Host ""
  Write-Host ">>> PASO 6/6: Arranque del servicio y verificacion de salud" -ForegroundColor Cyan

  $portBlocker = Test-PortBlocked -Port $listenPort
  if ($portBlocker) {
    throw "El puerto $listenPort ya esta ocupado por otro programa: $portBlocker. Cerralo o elegi otro puerto."
  }

  Start-Service -Name $serviceName -ErrorAction Stop
  $health = Wait-ForAgentHealth -Port $listenPort
  if ($null -eq $health) {
    throw "El servicio no respondio OK en http://127.0.0.1:${listenPort}/health luego de 30 segundos. Revisa $agentLogPath."
  }
  Add-StepResult -StepId "6_arranque_y_salud" -Title "Servicio iniciado y /health OK" -Status "ok" -Detail ("version=$($health.version) printers=$($health.printers.Count)")

  if ($GuiSetup) {
    Show-GuiPrinterWizard -CfgJson $cfgJson -ConfigPath $configPath -ListenPort $listenPort -ServiceName $serviceName
  } elseif ($WebSetup) {
    Write-Log -Level "INFO" -Message "Abriendo asistente web en http://127.0.0.1:${listenPort}/#setup ..."
    try {
      Start-Process "http://127.0.0.1:${listenPort}/#setup"
      Add-StepResult -StepId "6_asistente_web" -Title "Asistente web abierto en navegador" -Status "ok" -Detail "http://127.0.0.1:${listenPort}/#setup"
    } catch {
      Write-Log -Level "WARN" -Message "No se pudo abrir el navegador automaticamente: $($_.Exception.Message)"
    }
  } elseif (-not $NonInteractive -and $cfgJson.printers.Count -gt 0) {
    if (Read-YesNo -Prompt "Queres enviar una impresion de prueba ahora para validar?" -DefaultYes $true) {
      $firstPrinter = $cfgJson.printers[0]
      $retryTest = $true
      while ($retryTest) {
        $retryTest = $false
        try {
          $testBody = @{
            printer_id = $firstPrinter.id
            template   = "kitchen"
            header     = @{
              order_number  = 0
              customer_name = "PRUEBA INSTALADOR"
              delivery_type = "take_away"
            }
            items      = @(
              @{ qty = 1; name = "Test de instalacion TomaPedidos" }
            )
          } | ConvertTo-Json -Depth 6
          $printResp = Invoke-RestMethod -Uri "http://127.0.0.1:${listenPort}/print" -Method Post -ContentType "application/json; charset=utf-8" -Body $testBody -TimeoutSec 10
          Add-StepResult -StepId "6_prueba_impresion" -Title "Ticket de prueba enviado" -Status "ok" -Detail "job_id=$($printResp.id) printer=$($firstPrinter.id) type=$($firstPrinter.type)"
          $printedOk = Read-YesNo -Prompt "Salio el ticket de prueba en la impresora '$($firstPrinter.name)'?" -DefaultYes $true
          if (-not $printedOk) {
            Add-StepResult -StepId "6_prueba_validacion" -Title "El operador indico que no salio el ticket" -Status "warn" -Detail "printer=$($firstPrinter.id) type=$($firstPrinter.type)"
            if ($firstPrinter.type -eq "usb" -and (Read-YesNo -Prompt "Queres probar cambiando el modo de '$($firstPrinter.name)' a 'usb-gdi' y reintentar?" -DefaultYes $true)) {
              $firstPrinter.type = "usb-gdi"
              Write-Utf8NoBom -Path $configPath -Content ($cfgJson | ConvertTo-Json -Depth 10)
              Restart-Service -Name $serviceName -ErrorAction Stop
              $null = Wait-ForAgentHealth -Port $listenPort
              $retryTest = $true
            }
          } else {
            Add-StepResult -StepId "6_prueba_validacion" -Title "Impresion fisica confirmada por operador" -Status "ok" -Detail "printer=$($firstPrinter.id)"
          }
        } catch {
          Add-StepResult -StepId "6_prueba_impresion" -Title "Fallo envio de ticket de prueba" -Status "warn" -Detail $_.Exception.Message
        }
      }
    }
  }

  # Cleanup legacy files only after healthy startup.
  foreach ($legacyPath in @($legacyDest, $legacyConfig)) {
    if (Test-Path $legacyPath) {
      Remove-Item -Force -ErrorAction SilentlyContinue $legacyPath
    }
  }
  if (Test-Path -Path $legacyJobsPattern) {
    Remove-Item -Force -ErrorAction SilentlyContinue $legacyJobsPattern
  }

  $script:Report.status = "ok"
  Save-InstallReport -TargetReportUrl $ReportUrl

  Write-Host ""
  Write-Host "============================================================" -ForegroundColor Green
  Write-Host "  INSTALACION COMPLETA Y FUNCIONANDO" -ForegroundColor Green
  Write-Host "============================================================" -ForegroundColor Green
  Write-Host "  Panel Web:    http://127.0.0.1:${listenPort}"
  Write-Host "  Diagnostico:  http://127.0.0.1:${listenPort}/diagnostics"
  Write-Host "  Config:       $configPath"
  Write-Host "  Logs:         $logsDir"
  Write-Host "  Reporte:      $reportFile"
  Write-Host ""
  exit 0
} catch {
  $errMsg = $_.Exception.Message
  Add-StepResult -StepId $script:CurrentStep -Title "Paso detenido por error" -Status "failed" -Detail $errMsg
  $script:Report.status = "failed"
  $script:Report.failed_step = $script:CurrentStep
  $script:Report.error = $errMsg

  if ($hadService -and $oldBinPath -and ($script:CurrentStep -eq "5_servicio_y_acl" -or $script:CurrentStep -eq "6_arranque_y_salud")) {
    Write-Log -Level "WARN" -Message "Intentando rollback al servicio previo..."
    $rbOk = Restore-PreviousService -Name $serviceName -BinaryPath $oldBinPath -InstalledBinary $dest -BinaryBackup $installedBinaryBackup -Port $listenPort
    if ($rbOk) {
      Add-StepResult -StepId "rollback" -Title "Rollback al servicio previo" -Status "ok" -Detail "Backup: $backupDir"
    } else {
      Add-StepResult -StepId "rollback" -Title "Rollback al servicio previo" -Status "failed" -Detail "Backup manual en: $backupDir"
    }
  }

  Save-InstallReport -TargetReportUrl $ReportUrl
  Write-Host ""
  Write-Host "============================================================" -ForegroundColor Red
  Write-Host "  LA INSTALACION SE TRANCO EN: $($script:CurrentStep)" -ForegroundColor Red
  Write-Host "  Codigo de soporte: $($script:Report.support_code)" -ForegroundColor Yellow
  Write-Host "  Error: $errMsg" -ForegroundColor Red
  Write-Host "  Log:   $logFile" -ForegroundColor Yellow
  Write-Host "============================================================" -ForegroundColor Red

  if ($GuiSetup -or $WebSetup) {
    try {
      Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
      $msg = "No se pudo completar la instalacion de TomaPedidos Print Agent.`n`n" +
             "Paso: $($script:CurrentStep)`n" +
             "Codigo de soporte: $($script:Report.support_code)`n" +
             "Error: $errMsg`n`n" +
             "Log: $logFile"
      [void][System.Windows.Forms.MessageBox]::Show($msg, "TomaPedidos Print Agent - Error", [System.Windows.Forms.MessageBoxButtons]::OK, [System.Windows.Forms.MessageBoxIcon]::Error)
    } catch {}
  }
  exit 1
}
