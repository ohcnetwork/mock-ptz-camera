# Mock PTZ Camera

A software-defined mock PTZ (Pan-Tilt-Zoom) IP camera with RTSP streaming, ONVIF control, and a built-in web UI. By default it renders a perspective view into a 360° panoramic image that responds to PTZ commands, streams the result as H.264 over RTSP, and provides a low-latency H.264 preview in the browser via WebCodecs.

![Mock PTZ Camera Web UI](docs/assets/screenshot1.png)

## Features

- **RTSP Streaming** — H.264 stream via gortsplib with Digest authentication
- **ONVIF Services** — Device, Media, PTZ, and Events (PullPoint) with WS-UsernameToken auth
- **PTZ Control** — ContinuousMove, AbsoluteMove, RelativeMove, Stop, Presets
- **360° Panoramic Renderer** — Simulates a PTZ camera navigating an equirectangular 360° panoramic image with perspective projection (default)
- **Test Pattern Renderer** — Built-in test pattern with crosshair and zoom indicator (no video file needed)
- **Web UI** — Low-latency H.264 live preview (via WebCodecs VideoDecoder) with D-pad, zoom, speed, absolute move, and preset controls over WebSocket
- **API Test UI** — Built-in ONVIF endpoint tester with manual and automated test modes
- **WS-Discovery** — Responds to ONVIF probe messages on `239.255.255.250:3702`
- **Unified Auth** — Single credential set for ONVIF, RTSP, and Web UI (Basic auth)

## Architecture

```
[Test Pattern / Pano Renderer] ← PTZ State ← ONVIF PTZ / WebSocket commands
          ↓
   [FFmpeg Encoder] → H.264 NALUs → [AUHub fan-out]
                                        ↓           ↓
                              [RTP Packetizer]   [WebSocket]
                                    ↓                ↓
                              RTSP Server        Web UI (WebCodecs)
```

Three goroutines drive the pipeline:

1. **Render loop** (`renderer.RenderLoop`) — Ticks at the configured FPS. Each tick reads the current PTZ position, renders a frame as raw RGB24, and writes it to the FFmpeg encoder.

2. **AU Hub** (`web.AUHub.Run`) — Reads H.264 access units from the encoder's output channel and fans them out to all subscribers (RTSP and WebSocket clients) via non-blocking sends.

3. **Stream loop** (`rtsp.StreamLoop`) — Subscribes to the AU Hub, wraps access units into RTP packets via `rtph264.Encoder`, and writes them to connected RTSP clients through gortsplib.

## Quick Start

### Docker (recommended)

```bash
docker compose up --build
```

On **macOS** (Docker Desktop), set `HOST_IP` to your Mac's LAN IP so ONVIF clients can reach the camera. WS-Discovery (multicast) does not work through Docker Desktop's VM — use direct ONVIF HTTP access instead.

```bash
HOST_IP=192.168.1.100 docker compose up --build
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
| `BITRATE` | `2M` | H.264 CBR target bitrate (e.g. `1M`, `2M`, `4M`) |
| `LOG_LEVEL` | `info` | Log verbosity (`debug`, `info`, `warn`, `error`) |
| `RENDERER` | `pano` | Renderer type: `pano` or `testpattern` |
| `PANO_IMAGE` | `assets/default_pano.jpg` | Path to equirectangular panoramic image (used when `RENDERER=pano`) |
| `HOST_IP` | *(auto-detect)* | IP address advertised in ONVIF/RTSP URLs. Set when running in Docker or when auto-detection picks the wrong interface |

## Endpoints

- **Web UI**: `http://<host>:8080/` (Basic auth)
- **API Test UI**: `http://<host>:8080/test` (Basic auth)
- **Video WebSocket**: `ws://<host>:8080/ws/video` (H.264 Annex B via WebCodecs)
- **Control WebSocket**: `ws://<host>:8080/ws` (PTZ commands)
- **RTSP Stream**: `rtsp://<host>:8554/stream`
- **ONVIF Device Service**: `http://<host>:8080/onvif/device_service`
- **ONVIF Media Service**: `http://<host>:8080/onvif/media_service`
- **ONVIF PTZ Service**: `http://<host>:8080/onvif/ptz_service`
- **ONVIF Events Service**: `http://<host>:8080/onvif/events_service`

## Testing

### Web UI

Open `http://localhost:8080` in a browser (credentials: `admin` / `admin`). The UI provides a low-latency H.264 live preview and PTZ controls including D-pad, zoom, speed slider, absolute move, presets, and keyboard shortcuts. Requires a browser with WebCodecs support (Chrome 94+, Edge 94+, Safari 16.4+).

**Keyboard shortcuts:**

| Key | Action |
|---|---|
| Arrow keys | Relative pan/tilt |
| `+` / `-` | Relative zoom in/out |
| `H` | Home (go to 0, 0, 0) |
| `Space` | Stop movement |

### API Test UI

Open `http://localhost:8080/test` to access the built-in ONVIF endpoint tester. It supports two modes:

**Manual mode** — Select any ONVIF endpoint from the sidebar, configure parameters, and send individual SOAP requests. The request and response XML are shown side-by-side with syntax highlighting. Supports all auth modes (None, PasswordText, PasswordDigest).

**Automated mode** — Click "Run All" to switch to the automated test runner, which executes a suite of 30+ tests across all ONVIF services in sequence:

- **Device Service** — GetSystemDateAndTime, GetDeviceInformation, GetServices, GetCapabilities, GetScopes
- **Media Service** — GetProfiles, GetProfile, GetStreamUri, GetVideoSources, encoder configurations
- **PTZ Service** — GetStatus, GetNodes, GetConfigurations, AbsoluteMove, RelativeMove, ContinuousMove, Stop, preset CRUD (Set → Get → Goto → Remove)
- **Events & Subscription** — GetEventProperties, CreatePullPointSubscription, PullMessages, Renew, Unsubscribe

Each test validates HTTP status, checks for unexpected SOAP faults, and asserts expected content in the response. Results show real-time pass/fail status, timing, and a progress bar. Click any test row to inspect the full request/response XML.

The test page also includes a **WS-Discovery** section with copyable Probe XML and a ready-to-run Python CLI command for testing UDP multicast discovery.

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
│   ├── renderloop.go    # Frame render loop (drives encoder)
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
│   ├── auhub.go         # H.264 access unit fan-out hub (encoder → RTSP + WebSocket)
│   └── static/
│       ├── index.html   # Camera control UI (single-page, shadcn-inspired dark theme)
│       ├── test.html    # API test UI with manual and automated test runner
│       ├── common.css   # Shared design system (CSS variables, layout, components)
│       └── common.js    # Shared JS utilities (escapeHtml, formatXml)
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
# Use the default pano renderer with the bundled default image
./mock-ptz-camera

# Use a custom equirectangular image
PANO_IMAGE=/path/to/your/pano.jpg ./mock-ptz-camera

# Use the test pattern renderer instead
RENDERER=testpattern ./mock-ptz-camera
```

The bundled default image (`assets/default_pano.jpg`) is [Hospital Room 2](https://polyhaven.com/a/hospital_room_2) from [Poly Haven](https://polyhaven.com), licensed under [CC0 (Public Domain)](https://polyhaven.com/license).

## License

MIT
