# Manual de uso — TomaPedidos Print Agent

## 1. ¿Qué es?

El Print Agent es el servicio local que imprime las comandas en la/s
impresora/s térmica/s de tu comercio. Corre en la misma PC donde está
conectada la impresora (USB o red). La SaaS (tomaPedidos en la nube) NO
imprime nada: el navegador del cajero le manda el trabajo de impresión al
agente, que escucha en `http://127.0.0.1:4510`. Todo el tráfico es local,
nunca sale a internet.

**¿Por qué un agente separado?**  
Porque la SaaS corre en Vercel y no puede ver las impresoras físicas de
tu local. El agente vive en la PC del cajero y habla directamente con el
hardware.

---

## 2. Instalación

### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/tomapedidos/print-agent/main/install/install.sh | bash
```

Esto:
1. Descarga el binario adecuado para tu SO y arquitectura.
2. Crea la carpeta `~/.config/tomapedidos/`.
3. Genera un archivo de configuración de ejemplo en `~/.config/tomapedidos/printers.json`.
4. Registra el agente como servicio (launchd en macOS, systemd en Linux).
5. El servicio arranca inmediatamente y cada vez que inicies sesión.

### Windows PowerShell (como administrador)

```powershell
Invoke-WebRequest -Uri https://raw.githubusercontent.com/tomapedidos/print-agent/main/install/install.ps1 | Invoke-Expression
```

Esto:
1. Descarga `print-agent-windows-amd64.exe` a `%APPDATA%\tomapedidos\`.
2. Crea una config de ejemplo.
3. Registra el servicio con `sc create`.
4. Lo arranca.

### Descarga manual

Si preferís bajar el binario a mano, entrá a la página de
[Releases](https://github.com/tomapedidos/print-agent/releases) y elegí
el `.exe`/`.bin` que corresponda a tu SO.

---

## 3. Configuración

El archivo `printers.json` (por defecto en `~/.config/tomapedidos/` o
`%APPDATA%\tomapedidos\`) define qué impresoras existen y cómo hablarles.
El agente genera uno de ejemplo en el primer arranque si no lo encuentra.

### Estructura mínima

```json
{
  "port": 4510,
  "bind": "127.0.0.1",
  "tenant": {
    "id": "TU_TENANT_ID",
    "branch_id": "TU_BRANCH_ID"
  },
  "printers": [
    {
      "id": "cocina",
      "name": "Cocina",
      "type": "network",
      "host": "192.168.1.30",
      "port": 9100,
      "code_page": "cp850",
      "chars_per_line": 42,
      "cut": "partial"
    },
    {
      "id": "caja",
      "name": "Caja",
      "type": "usb",
      "system_name": "EPSON_TM_T20III",
      "code_page": "cp850",
      "chars_per_line": 42,
      "cut": "partial"
    }
  ],
  "queue": {
    "max_retries": 3,
    "retry_backoff_ms": [1000, 3000, 10000],
    "dedup_window_ms": 300000,
    "persist_path": "./data/jobs.db"
  },
  "panel": {
    "enabled": true,
    "pin": "0000"
  }
}
```

### Campos importantes

| Campo | Significado |
|---|---|
| `tenant.id` | ID del comercio en tomaPedidos (copialo de `/admin/settings/print-agent`). |
| `tenant.branch_id` | ID de la sucursal (copialo de la misma página). |
| `printers[].id` | Identificador lógico. **Debe coincidir exactamente** con el `printerName` que configures en `/admin/settings/comandas` de tomaPedidos. |
| `printers[].type` | `network` (TCP 9100), `usb` (CUPS/Windows Spooler) o `file` (debug, escribe a un archivo). |
| `printers[].code_page` | `cp850` cubre ñ y acentos para Argentina. `cp437` para USA. `cp1252` para Windows Latin-1. |
| `printers[].cut` | `partial` (corte parcial, el más común), `full` (corte total), `none`. |
| `queue.max_retries` | Cuántas veces reintenta antes de marcar el job como `failed`. |
| `queue.persist_path` | Ruta al archivo SQLite donde se guardan los jobs (vacío = solo en memoria). |
| `panel.pin` | PIN de 4 dígitos para acceder al panel web. |

### ¿De dónde saco `tenant.id` y `branch_id`?

Abrí tomaPedidos en el navegador como cajero/admin, andá a
**Configuración → Agente de impresión**. Ahí ves los IDs con botones
"Copiar". Pegalos en el `tenant` del JSON.

---

## 4. Operación diaria

### 4.1 Panel del agente

Abrí `http://127.0.0.1:4510` en cualquier navegador de la misma PC.

**Login**: ingresá el PIN del panel (default `0000`). Cambialo en
**Settings** la primera vez.

**Dashboard**: muestra el estado de cada impresora (online/offline/error),
la profundidad de la cola y la hora del último print exitoso.

**Impresoras**: acá das de alta, editas o eliminás impresoras. Podés hacer
**Test print** para verificar que la conexión está bien.

**Jobs**: historial de trabajos de impresión. Podés filtrar por estado
(failed, queued, printed), reimprimir un job viejo o cancelar uno
pendiente.

**Settings**: cambiar el PIN.

### 4.2 Vincular con tomaPedidos

1. Asegurate de que los IDs del `tenant` en `printers.json` matchean con
   los que ves en `/admin/settings/print-agent`.
2. Andá a **Configuración → Comandas** en tomaPedidos.
3. Si el agente está corriendo, el campo "Impresora de Salida" muestra un
   dropdown con los `id` de las impresoras del agente. Si no ves el
   dropdown, clickeá **Actualizar lista**.
4. Elegí la impresora destino para cada comanda (Cocina, Caja, Barra).
5. Asigná las categorías de productos a cada comanda.

### 4.3 Imprimir un pedido

1. Desde el panel de tomaPedidos, localizá el pedido.
2. Hacé click en el ícono de la **impresora**.
3. El navegador computa el ruteo (categoría → comanda → printer_id) y
   manda los jobs al agente.
4. Aparece un toast verde confirmando "Comanda enviada" y listando las
   impresoras destino.
5. Si algo falla, el toast es rojo y te dice el motivo (agente caído,
   impresora offline, 404, etc.).

### 4.4 ¿Y si el indicador del header está rojo?

Un dot rojo en el header del panel admin de tomaPedidos significa que la
PC no puede contactar al agente en `http://127.0.0.1:4510`. Verificá:

1. ¿El agente está corriendo?  
   `print-agent status-svc` (o `sc query com.tomapedidos.print-agent` en
   Windows).
2. ¿El binario está en el PATH correcto? Revisá que el plist / unit /
   servicio apunte al ejecutable real.
3. ¿El puerto 4510 está libre? El agente lo abre en loopback solamente.
4. ¿El firewall del SO está bloqueando `127.0.0.1:4510`? Normalmente no,
   pero algunos antivirus restringen loopback.

### 4.5 Reimprimir una comanda

Desde el panel del agente (`http://127.0.0.1:4510`) → **Jobs** → buscá el
job → click en **Reprint**.

O directamente desde tomaPedidos: volvé a clickear el ícono de la
impresora en el pedido. El agente genera un nuevo `job_id`, así que no
hay riesgo de dedup (el anterior ya se imprimió y fue removido).

### 4.6 Abrir el cajón de dinero

El botón **Test print** en el panel del agente también puede configurarse
con `"open_cash_drawer": true` en el job. La impresora emite un pulso por
el puerto RJ12 y el cajón se abre.

---

## 5. Comandos del binario

| Comando | Qué hace |
|---|---|
| `print-agent start --config <path>` | Corre en foreground (modo manual / debug). |
| `print-agent version` | Muestra la versión y el commit de build. |
| `print-agent init-config [path]` | Escribe una config de ejemplo en `path`. |
| `print-agent doctor` | Diagnóstico rápido: SO, loopback, versión de Go. |
| `print-agent install --config <path>` | Instala como servicio del SO. |
| `print-agent uninstall` | Desinstala el servicio. |
| `print-agent start-svc` | Arranca el servicio. |
| `print-agent stop-svc` | Detiene el servicio. |
| `print-agent status-svc` | Muestra `running` o `stopped`. |
| `print-agent help` | Esta ayuda. |

---

## 6. Troubleshooting

### "Agente local no disponible" al hacer click en Imprimir

El navegador no pudo contactar `http://127.0.0.1:4510`. Verificá que el
agente esté corriendo (`status-svc`) y que el puerto no esté tomado por
otro proceso.

### "La impresora X no está configurada en el agente local"

El `printerName` en la comanda de tomaPedidos no coincide con ningún `id`
de `printers.json`. Abrí el panel del agente, fijate los ids de
impresoras que tenés, y corregí la comanda.

### "lpstat -p ZZZ: nombre de destino no válido" (macOS/Linux)

El `system_name` en la config no existe en CUPS. Ejecutá `lpstat -p` en
una terminal para ver las impresoras instaladas.

### "print /d:XXX … not found" (Windows)

La impresora no está instalada en el Spooler de Windows con ese nombre
exacto. Verificá con `Get-Printer`.

### La impresora imprime símbolos raros en vez de texto

Probablemente el `code_page` no es el correcto. Probá con `cp850` (el
default para Latinoamérica). Si la impresora es muy vieja, `cp437`.

### La impresora no corta el papel

Asegurate de que el campo `cut` en la config sea `partial` o `full`. No
todas las impresoras soportan `full`. Probá con `partial` primero.

### Quiero ver qué bytes se están enviando

Cambiá el tipo de la impresora a `file` temporalmente y apuntá
`file_path` a un archivo. Después de imprimir, abrí el archivo con
`xxd file.bin` para inspeccionar los bytes ESC/POS.

---

## 7. Endpoints HTTP (referencia)

Todos los endpoints escuchan únicamente en `127.0.0.1`. Ninguno es
accesible desde la red.

| Método | Path | Auth | Descripción |
|---|---|---|---|
| `GET` | `/health` | no | Liveness + status de impresoras. |
| `GET` | `/printers` | no | Lista de impresoras configuradas. |
| `POST` | `/print` | no | Imprimir un job a una impresora. |
| `POST` | `/print/batch` | no | Imprimir varios jobs (batch). |
| `POST` | `/cash-drawer/kick` | no | Abrir cajón de dinero. |
| `GET` | `/jobs` | no | Listar jobs (filtrable por status). |
| `GET` | `/jobs/{id}` | no | Detalle de un job. |
| `POST` | `/jobs/{id}/reprint` | no | Reencolar un job. |
| `DELETE` | `/jobs/{id}` | no | Cancelar un job pendiente. |
| `GET` | `/events` | no | WebSocket de eventos en vivo. |
| `POST` | `/auth/login` | — | Login del panel (PIN). |
| `POST` | `/auth/logout` | — | Logout del panel. |
| `GET` | `/config` | PIN | Ver configuración completa. |
| `PUT` | `/config` | PIN | Reemplazar configuración. |
| `GET` | `/printers/detect` | PIN | Detectar impresoras del OS. |

---

## 8. Panel del agente (referencia visual rápida)

```
http://127.0.0.1:4510
┌──────────┬──────────────────────────────────────────────┐
│ Sidebar  │  Content                                     │
│          │                                              │
│ Dashboard│  ┌─────────────┐  ┌─────────────┐            │
│ Impresoras│  │ cocina      │  │ caja        │            │
│ Jobs     │  │ online      │  │ offline     │            │
│ Settings │  │ cola: 0     │  │ cola: 2     │            │
│          │  │ last: 14:32 │  │ last: --    │            │
│          │  └─────────────┘  └─────────────┘            │
│          │                                              │
│  Logout  │  Uptime: 3h · Tenant: tenant_abc/suc_1       │
└──────────┴──────────────────────────────────────────────┘
```

---

## 9. Preguntas frecuentes

**¿Necesito instalar el agente en cada PC del comercio?**  
Sí. Cada PC que tenga una impresora conectada corre su propio agente. Si
tenés la caja en una PC y la cocina en otra, cada una instala su agente
por separado. La SaaS no coordina entre agentes (cada uno imprime en sus
propias impresoras, configuradas en su propio `printers.json`).

**¿Puedo imprimir desde un celular o tablet?**  
No. El agente corre en una PC de escritorio y escucha en loopback. La app
móvil del cajero no puede alcanzar `127.0.0.1` de la PC.

**¿Se puede imprimir automáticamente cuando entra un pedido online?**  
En esta versión, no. La impresión la dispara el cajero manualmente con el
botón "Imprimir". El auto-print requiere cambios en la SaaS (que el
browser escuche eventos realtime y dispare el print sin intervención
humana) y está planificado para una versión futura.

**¿El agente necesita internet?**  
No. Solo habla con `127.0.0.1` (loopback). La descarga inicial del
binario sí necesita internet, pero después de instalado funciona 100%
offline.

**¿Qué impresoras son compatibles?**  
Cualquier impresora térmica ESC/POS que acepte comandos por TCP (puerto
9100) o esté instalada en el sistema operativo como cola de impresión.
Marcas probadas: Epson TM-T20, TM-T88, XPrinter XP-365B, GOOJPRT, SAT,
Bematech. Si tu impresora imprime desde el block de notas, debería
funcionar con el agente configurando `type: "usb"`.

**¿Cómo actualizo el agente a una versión nueva?**  
1. `print-agent stop-svc`
2. Reemplazá el binario por la versión nueva.
3. `print-agent start-svc`
4. La cola de jobs pendientes se recarga del archivo SQLite
   automáticamente si tenés `persist_path` configurado.
