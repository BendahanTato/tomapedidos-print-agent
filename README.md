# TomaPedidos Print Agent

Servicio local de impresión para TomaPedidos. Recibe trabajos de impresión
desde el navegador del cajero (que habla con la SaaS vía Vercel) y los
envía a impresoras térmicas ESC/POS por red (TCP 9100), USB (vía spooler
del SO) o un archivo de debug.

## Características (M1)

- Binario único sin dependencias externas en runtime (CGO deshabilitado).
- HTTP bound a `127.0.0.1:4510` (loopback, jamás expuesto a la LAN).
- Endpoints: `GET /health`, `GET /printers`, `POST /print`, `POST /print/batch`,
  `POST /cash-drawer/kick`, `GET /jobs`, `GET /jobs/:id`,
  `POST /jobs/:id/reprint`, `DELETE /jobs/:id`.
- Plantilla de cocina (sin precios, modificadores, observaciones).
- Generador ESC/POS nativo (init, bold, alineación, doble tamaño, code
  pages CP850/1252/etc., corte, apertura de cajón).
- Cola FIFO por printer con reintentos (backoff configurable).
- Detección de impresoras: red (TCP 9100) y archivo (debug).
- Heartbeat cada 10s para refrescar `status` en `/health` y `/printers`.
- Logs estructurados JSON a stdout.
- Panel web mínimo en `/` (M5 traerá el panel completo).

## Roadmap

| Fase | Status | Entrega |
|---|---|---|
| M1 | hecho | Agente mínimo con red + file + queue + retries |
| M2 | pendiente | USB vía spooler del OS (CUPS / Windows Spooler) |
| M3 | pendiente | Cola persistente con SQLite |
| M4 | pendiente | Persistencia y hot reload de config |
| M5 | pendiente | Panel web completo (login, CRUD, dashboard) |
| M6 | pendiente | WebSocket `/events` para status en vivo |
| M7 | pendiente | Service mode (launchd / systemd / SCM) |
| M8 | pendiente | Distribución (binarios firmados + instaladores) |
| M9 | pendiente | Hardening (rate limit, métricas, logs con rotación) |

## Compilar

```bash
# Binario para el OS actual
go build -o print-agent ./cmd/print-agent

# Cross-compile
GOOS=darwin  GOARCH=arm64 go build -o dist/print-agent-darwin-arm64  ./cmd/print-agent
GOOS=darwin  GOARCH=amd64 go build -o dist/print-agent-darwin-amd64  ./cmd/print-agent
GOOS=linux   GOARCH=amd64 go build -o dist/print-agent-linux-amd64    ./cmd/print-agent
GOOS=windows GOARCH=amd64 go build -o dist/print-agent-windows-amd64.exe ./cmd/print-agent
```

## Uso

```bash
# 1. Generar un config de ejemplo
./print-agent init-config --config ./configs/printers.json

# 2. Editar el config con tus impresoras
$EDITOR ./configs/printers.json

# 3. Correr el agente (foreground)
./print-agent start --config ./configs/printers.json

# 4. Verificar
curl -s http://127.0.0.1:4510/health | jq

# 5. Mandar un trabajo de prueba
curl -s -X POST http://127.0.0.1:4510/print/batch \
  -H "Content-Type: application/json" \
  -d '{"jobs":[{"printer_id":"cocina","header":{"order_number":42,"customer_name":"Juan","delivery_type":"take_away"},"items":[{"qty":2,"name":"Pizza Muzza","modifiers":["Extra queso"]}],"options":{"cut":"partial"}}]}'
```

## Configuración

El archivo JSON tiene esta forma:

```json
{
  "port": 4510,
  "bind": "127.0.0.1",
  "tenant": { "id": "tenant_xxx", "branch_id": "branch_yyy" },
  "printers": [
    { "id": "cocina", "name": "Cocina", "type": "network",
      "host": "192.168.1.30", "port": 9100, "code_page": "cp850",
      "chars_per_line": 42, "cut": "partial" }
  ],
  "queue": { "max_retries": 3, "retry_backoff_ms": [1000, 3000, 10000],
             "dedup_window_ms": 300000 },
  "panel": { "enabled": true, "pin": "0000" }
}
```

Tipos de printer soportados:

- `network`: TCP raw socket al `host:port` (típico 9100).
- `usb`: pendiente (M2) — usará el spooler del OS (CUPS / Windows Spooler).
- `file`: escribe los bytes a `file_path` (modo debug / emulador).

## API

Ver [docs/api.md](docs/api.md) cuando se escriba. Mientras tanto, la lista
completa de endpoints está en el banner HTML de `GET /` y los structs
viven en `internal/server/`.

## Licencia

Propietaria. © TomaPedidos.
