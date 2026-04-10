# Mock PTZ Camera

A software-defined mock PTZ (Pan-Tilt-Zoom) IP camera with RTSP streaming, ONVIF control, and a built-in web UI. It renders a test pattern that responds to PTZ commands, streams the result as H.264 over RTSP, and provides an MJPEG preview in the browser.

## Features

- **RTSP Streaming** — H.264 stream via gortsplib with Digest authentication
- **ONVIF Services** — Device, Media, PTZ, and Events (PullPoint) with WS-UsernameToken auth
- **PTZ Control** — ContinuousMove, AbsoluteMove, RelativeMove, Stop, Presets
- **Test Pattern Renderer** — Built-in test pattern with crosshair and zoom indicator (no video file needed)
- **360° Panoramic Renderer** — Simulates a PTZ camera navigating an equirectangular 360° panoramic image with perspective projection
- **Web UI** — MJPEG live preview with D-pad, zoom, speed, absolute move, and preset controls over WebSocket
- **WS-Discovery** — Responds to ONVIF probe messages on `239.255.255.250:3702`
- **Unified Auth** — Single credential set for ONVIF, RTSP, and Web UI (Basic auth)

## Architecture

```
[Test Pattern / Pano Renderer] ← PTZ State ← ONVIF PTZ / WebSocket commands
          ↓
   [FFmpeg Encoder] → H.264 NALUs → [RTP Packetizer] → RTSP Server → clients
          ↓
   [JPEG Encoder] → MJPEG frames → Web UI (browser)
```

Two goroutines drive the pipeline:

1. **Render loop** (`renderer.RenderLoop`) — Ticks at the configured FPS. Each tick reads the current PTZ position, renders a test-pattern frame as raw RGB24, writes it to the FFmpeg encoder for H.264, and simultaneously JPEG-encodes the same RGB buffer for the web MJPEG stream. The two encodings serve different consumers (RTSP vs browser) and encoding JPEG directly from the raw pixels is cheaper than decoding H.264 back.

2. **Stream loop** (`rtsp.StreamLoop`) — Consumes H.264 access units (NALUs) from the encoder's output channel, wraps them into RTP packets via `rtph264.Encoder` and writes them to connected RTSP clients through gortsplib.

## Quick Start

### Docker (recommended)

```bash
docker compose up --build
```

### From source

Requires Go 1.26+ and FFmpeg installed.

```bash
go build -o mock-ptz-camera .
./mock-ptz-camera
```

## Configuration

All settings are configurable via environment variables:

| Variable | Default | Description |
|---|---|---|
| `CAMERA_USER` | `admin` | Username for ONVIF, RTSP, and web auth |
| `CAMERA_PASS` | `admin` | Password for ONVIF, RTSP, and web auth |
| `RTSP_PORT` | `8554` | RTSP server port |
| `WEB_PORT` | `8080` | Web UI and ONVIF HTTP server port |
| `WIDTH` | `1280` | Output resolution width |
| `HEIGHT` | `720` | Output resolution height |
| `FPS` | `30` | Output frame rate |
| `LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `RENDERER` | `pano` | Renderer type: `testpattern` or `pano` |
| `PANO_IMAGE` | `assets/default_pano.jpg` | Path to equirectangular panoramic image (used when `RENDERER=pano`) |

## Endpoints

- **Web UI**: `http://<host>:8080/` (Basic auth)
- **MJPEG Stream**: `http://<host>:8080/api/stream` (Basic auth)
- **WebSocket**: `ws://<host>:8080/ws`
- **RTSP Stream**: `rtsp://<host>:8554/stream`
- **ONVIF Device Service**: `http://<host>:8080/onvif/device_service`
- **ONVIF Media Service**: `http://<host>:8080/onvif/media_service`
- **ONVIF PTZ Service**: `http://<host>:8080/onvif/ptz_service`
- **ONVIF Events Service**: `http://<host>:8080/onvif/events_service`

## Testing

### Web UI

Open `http://localhost:8080` in a browser (credentials: `admin` / `admin`). The UI provides live MJPEG preview and PTZ controls including D-pad, zoom, speed slider, absolute move, presets, and keyboard shortcuts.

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| Arrow keys | Relative pan/tilt |
| `+` / `-` | Relative zoom in/out |
| `H` | Home (go to 0, 0, 0) |
| `Space` | Stop movement |

### Play the RTSP stream

```bash
ffplay rtsp://admin:admin@localhost:8554/stream
# or
mpv rtsp://admin:admin@localhost:8554/stream
```

### ONVIF testing with curl

```bash
# Get device info (no auth required for GetSystemDateAndTime)
curl -X POST http://localhost:8080/onvif/device_service \
  -H "Content-Type: application/soap+xml" \
  -d '<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
    <s:Body><GetSystemDateAndTime xmlns="http://www.onvif.org/ver10/device/wsdl"/></s:Body>
  </s:Envelope>'
```

### ONVIF Device Manager

The camera is discoverable via WS-Discovery and compatible with ONVIF Device Manager and similar tools.

## Project Structure

```
├── main.go              # Entry point, pipeline orchestration
├── config/              # Environment-based configuration
├── auth/                # WS-UsernameToken + RTSP Digest validation
├── ptz/                 # PTZ state machine (position, velocity, presets)
├── renderer/
│   ├── encoder.go       # FFmpeg H.264 encoder subprocess
│   ├── renderloop.go    # Frame render loop (drives encoder + JPEG snapshot)
│   ├── jpeg.go          # RGB24 → JPEG conversion for MJPEG stream
│   ├── testpattern.go   # Test pattern image renderer
│   ├── pano.go          # 360° panoramic image renderer
│   ├── osd.go           # Shared OSD (crosshair, text overlay, flip)
│   └── font.go          # Embedded 5x7 bitmap font for OSD text
├── rtsp/
│   ├── server.go        # gortsplib RTSP server wrapper
│   └── streamloop.go    # NALU → RTP packetization loop
├── onvif/
│   ├── server.go        # HTTP SOAP router and auth middleware
│   ├── templates.go     # SOAP/XML response templates
│   ├── types.go         # SOAP namespace constants and template data types
│   ├── device.go        # ONVIF Device service
│   ├── media.go         # ONVIF Media service
│   ├── ptz.go           # ONVIF PTZ service
│   ├── events.go        # ONVIF Events (PullPoint subscriptions)
│   └── discovery.go     # WS-Discovery multicast responder
├── web/
│   ├── server.go        # HTTP server, route registration, auth middleware
│   ├── websocket.go     # WebSocket PTZ command handler
│   ├── mjpeg.go         # MJPEG multipart stream handler
│   ├── framestore.go    # Thread-safe JPEG frame store
│   └── static/
│       └── index.html   # Web UI (single-page, shadcn-inspired dark theme)
├── Dockerfile
├── docker-compose.yml
└── assets/
    └── default_pano.jpg # Default equirectangular panoramic image (CC0)
```

## Panoramic Renderer

The `pano` renderer projects a perspective camera view into a 360° equirectangular panoramic image. PTZ controls navigate the virtual camera through the sphere:

- **Pan** rotates the camera horizontally (full 360°)
- **Tilt** adjusts the camera's vertical angle
- **Zoom** narrows the field of view (from 90° down to ~4.5° at 20x)

```bash
# Use the pano renderer with the bundled default image
RENDERER=pano ./mock-ptz-camera

# Use a custom equirectangular image
RENDERER=pano PANO_IMAGE=/path/to/your/pano.jpg ./mock-ptz-camera
```

The bundled default image (`assets/default_pano.jpg`) is [Urban Street 04](https://polyhaven.com/a/urban_street_04) from [Poly Haven](https://polyhaven.com), licensed under [CC0 (Public Domain)](https://polyhaven.com/license).

## License

MIT
