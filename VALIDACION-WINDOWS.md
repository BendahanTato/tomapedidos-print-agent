# Checklist de validación en Windows (pre-producción)

Todo lo que sigue está sin validar desde macOS. Este checklist es el pase
previo a liberar: **una máquina real, con Windows en español y una impresora
conectada**. Marcá cada ítem antes de distribuir.

Requisitos de la máquina de prueba: Windows 10/11 en **español** (importante,
ver 3), impresora térmica ESC/POS y —si se usan— impresora de oficina,
PowerShell **elevado** y el release publicado con su `SHA256SUMS`.

> **Escenario real: sin firma de código.** Los ítems marcados con 🔒 sólo
> cambian con un certificado; todo el resto se puede ——y debe—— validar sin
> pagar nada. Sin firma es **esperable** ver el cartel de SmartScreen en la
> primera ejecución y que el explorador pregunte antes de conservar el archivo;
> eso no es una detección de antivirus, es falta de reputación de editor.

## Prueba express (~15 minutos, sin firma)

Objetivo: saber si **imprime** y si el servicio queda sano. Nada más.

```powershell
# 1. Traer el instalador y correrlo
$installer = "$env:TEMP\install-print-agent.ps1"
Invoke-WebRequest -Uri https://raw.githubusercontent.com/BendahanTato/tomapedidos-print-agent/main/install/install.ps1 -OutFile $installer -UseBasicParsing
powershell -ExecutionPolicy Bypass -File $installer

# 2. ¿Quedó vivo?
Invoke-RestMethod http://127.0.0.1:4510/health
Invoke-RestMethod http://127.0.0.1:4510/printers

# 3. Imprimir un ticket de prueba (con tildes)
$body = @{
  printer_id = "<id-del-paso-2>"
  header = @{ order_number = 9999; customer_name = "José Ñandú"; customer_address = "Dirección 45 • ¿Delivery?" }
  items = @(@{ qty = 2; name = "Hamburguesa doble"; notes = "Sin cebolla" })
} | ConvertTo-Json -Depth 6
Invoke-RestMethod -Uri http://127.0.0.1:4510/print -Method Post -Body $body -ContentType "application/json"

# 4. Ver el resultado y el log
Invoke-RestMethod http://127.0.0.1:4510/jobs
Get-Content "$env:ProgramData\Tomapedidos\logs\print-agent.log" -Tail 30
```

Resultado esperado: `/health` responde, el ticket sale con tildes correctas,
y el job figura `printed`. Si algo falla, pegá la salida del paso 4.

Si el script no arranca por política de PowerShell, usá la **instalación
manual** documentada en `MANUAL.md` (no requiere scripts).

---

## 0. Preparación

```powershell
# Estado del servicio tal como quedó instalado
sc.exe qc com.tomapedidos.print-agent
Get-CimInstance Win32_Service -Filter "Name='com.tomapedidos.print-agent'" |
  Select-Object Name, StartName, PathName, State
```

- [ ] `StartName` es `NT SERVICE\com.tomapedidos.print-agent` (**no** `LocalSystem`)
- [ ] `PathName` apunta a `%ProgramFiles%\Tomapedidos\PrintAgent\print-agent.exe`
- [ ] Archivos de estado en `%ProgramData%\Tomapedidos\{config,data,logs}`
- [ ] No queda nada en `%LOCALAPPDATA%\tomapedidos` ni `%APPDATA%\tomapedidos`

```powershell
# Un usuario estándar NO debe poder escribir el ejecutable
icacls.exe "$env:ProgramFiles\Tomapedidos\PrintAgent\print-agent.exe"
# El panel sólo escucha en loopback
netstat -ano | findstr 4510      # debe figurar 127.0.0.1:4510
```

- [ ] El ejecutable sólo es escribible por Administradores/SYSTEM
- [ ] El puerto 4510 no queda escuchando en `0.0.0.0`

---

## 1. Instalación limpia

```powershell
Invoke-WebRequest -Uri "<URL>/install.ps1" -OutFile "$env:TEMP\install.ps1"
& "$env:TEMP\install.ps1"
Invoke-RestMethod http://127.0.0.1:4510/health
```

- [ ] Instala sin errores y termina con el health check en verde
- [ ] Se generó `%ProgramData%\Tomapedidos\config\printers.json`
- [ ] `/health` responde; el panel abre en `http://127.0.0.1:4510`
- [ ] **Con firma configurada:** no aparece el cartel de SmartScreen
- [ ] **Sin firma:** aparece "editor desconocido" → esperado hasta firmar

**Integridad:** para verificar que sí aborta con un binario adulterado,
publicá temporalmente un `SHA256SUMS` desactualizado (o editá el archivo
descargado) y confirmá que el instalador falla y **no** continúa:

- [ ] Ante hash inválido: error claro, limpia temporales, no registra servicio

---

## 2. Migración desde una instalación vieja

Instalá primero el release **anterior** (exe en `%LOCALAPPDATA%`, servicio como
`LocalSystem`), configurá impresoras y dejá trabajos en la cola. Después corré
el instalador nuevo.

- [ ] Hace backup con timestamp en `%ProgramData%\Tomapedidos\backups\migration-*`
- [ ] Migra `printers.json` y `jobs.db` (y reescribe `persist_path`)
- [ ] **Reconfigura** el servicio en lugar de borrarlo (siguen nombre y estado)
- [ ] Levanta y pasa el health check
- [ ] Recién entonces borra los archivos viejos del perfil de usuario
- [ ] El backup incluye `service-before.txt` con la config previa

---

## 3. Rollback automático (Windows en español)

El punto crítico: `sc.exe qc` imprime las etiquetas **localizadas**
(`RUTA_DE_ARCHIVO_BINARIO` en vez de `BINARY_PATH_NAME`), por eso se lee el
`binPath` por WMI. Comprobalo forzando un fallo:

```powershell
# Ocupar el puerto para que el agente nuevo no pase el health check
# (alternativa: romper el JSON de printers.json validando el mensaje de error)
Stop-Service com.tomapedidos.print-agent
```

- [ ] Ante fallo, restaura automáticamente el binario y `obj= LocalSystem`
- [ ] El servicio restaurado pasa el health check
- [ ] Si el rollback también falla, imprime ruta del backup y comandos manuales
- [ ] Nunca sale "en silencio": siempre deja mensaje accionable

Escape hatch documentado (si una impresora compartida no acepta la cuenta
virtual):

```powershell
sc.exe config com.tomapedidos.print-agent obj= LocalSystem password= ""
Start-Service com.tomapedidos.print-agent
```

> Esto conserva el hardening de rutas (sigue fuera del perfil de usuario) y
> sólo vuelve a la identidad anterior.

---

## 4. Impresión real

- [ ] **Térmica USB (RAW):** ticket con tildes y eñes correctas
  (`Cliente: José Ñandú`, `Dirección`, `¿Delivery?`)
- [ ] **Térmica de red (9100):** idem
- [ ] QR, corte parcial y apertura de cajón funcionan
- [ ] **Impresora de oficina `usb-office` (TEXT):** tildes correctas — este era
  el bug de UTF-8 crudo; probar texto con varios acentos seguidos
- [ ] **`usb-gdi`:** sale por GDI sin caracteres raros
- [ ] Plantilla custom: ubicada junto al `.exe` es la que se usa (no la del CWD)
- [ ] **Sin duplicados:** con la impresora apagada, enviar un job, esperar el
  timeout y reencender; el job debe quedar **fallido** (no reintentado), y al
  reenviar imprime una sola vez

---

## 5. Observabilidad

- [ ] Existe `%ProgramData%\Tomapedidos\logs\print-agent.log` desde el arranque
      del servicio
- [ ] Errores de validación de config, SQLite y puerto en uso quedan logueados
- [ ] Rotación: al superar el tamaño, aparecen `.1`, `.2`, `.3` (no más)
- [ ] Un fallo de arranque deja rastro (antes: silencio absoluto bajo SCM)

---

## 6. Antivirus / reputación

> Sin firma, los dos primeros ítems son los únicos que importan. Lo demás
> 🔒 queda pendiente hasta que exista certificado.

- [ ] Defender: sin detecciones en el histórico tras la instalación
- [ ] Anotar **exactamente** qué mostró Windows al ejecutar por primera vez
      (cartel de SmartScreen / aviso del navegador / nada) y si alcanzó con
      “Más información → Ejecutar de todas formas”
- [ ] El instalador no creó exclusiones de AV (no debe: eso empeora el perfil)
- [ ] 🔒 Repetir con firma habilitada: SmartScreen limpio
- [ ] 🔒 `signtool verify /pa print-agent.exe`
- [ ] Si algún motor lo marca como malware (no “desconocido”): registrar el
      nombre del motor y el hash, y enviar el falso positivo

---

## Diagnóstico para compartir

Si algo falla, este bloque junta todo lo necesario para diagnosticar sin
pedir más ida y vuelta (PowerShell elevado):

```powershell
$svc = "com.tomapedidos.print-agent"
Get-CimInstance Win32_Service -Filter "Name='$svc'" | Select-Object Name, StartName, PathName, State, StartMode
Get-Service $svc | Select-Object Status
Invoke-RestMethod http://127.0.0.1:4510/health           2>$null | ConvertTo-Json
Invoke-RestMethod http://127.0.0.1:4510/printers         2>$null | ConvertTo-Json -Depth 6
Invoke-RestMethod "http://127.0.0.1:4510/jobs?limit=5"   2>$null | ConvertTo-Json -Depth 6
Get-Content "$env:ProgramData\Tomapedidos\logs\print-agent.log" -Tail 50
Get-Content "$env:ProgramData\Tomapedidos\config\printers.json"
netstat -ano | findstr 4510
Get-WinEvent -LogName System -MaxEvents 15 |
  Where-Object { $_.Message -like "*$svc*" } | Format-List TimeCreated, Message
```

---

## 7. Regresión operativa

- [ ] Reinicio de la máquina: el servicio arranca solo
- [ ] Desinstalación limpia (`print-agent uninstall`): el exe queda liberado
      (no queda "marcado para eliminar" hasta el reboot)
- [ ] Actualizar a un release nuevo sobre la instalación actual funciona igual
      que la migración del punto 2

---

### Criterio de salida

Liberar sólo si: instalación limpia ✅ + migración con rollback probados ✅ +
impresión real (física) de los perfiles que usa cada cliente ✅. Los ítems de
firma pueden quedar abiertos **si se documenta** que SmartScreen va a avisar
hasta completar el paso 6.
