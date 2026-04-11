# renderer

The `renderer` package is the frame production engine of the mock PTZ camera. It takes a 360° equirectangular panoramic image (or a synthetic test pattern), projects a virtual perspective camera into it based on the current PTZ state, encodes the result to H.264, and produces JPEG snapshots for the web preview.

## Architecture

```
┌──────────────┐     RGB24      ┌──────────┐    Annex B     ┌────────────┐
│  Renderer    │ ──────────────►│ Encoder  │ ──────────────►│ RTSP       │
│ (Pano/Test)  │                │ (FFmpeg) │   (NAL units)  │ StreamLoop │
└──────────────┘                └──────────┘                └────────────┘
       │
       │  RGB24 (every ~15fps)
       ▼
┌──────────────┐    JPEG
│ JPEGEncoder  │ ──────────────► FrameSink (MJPEG web stream)
└──────────────┘
```

The **RenderLoop** is the central coordinator. It runs a ticker at the configured FPS and on each tick:

1. Reads the current PTZ position from the shared `ptz.State`.
2. Calls the `Renderer` to produce an RGB24 frame.
3. Feeds the frame to the `Encoder` (RGB24 → I420 → FFmpeg stdin → H.264 NALUs).
4. At a capped ~15fps, asynchronously JPEG-encodes the frame for the MJPEG web preview.

## Files

| File | Purpose |
|------|---------|
| `renderloop.go` | Main frame production loop — ties renderer, encoder, and MJPEG sink together. |
| `pano.go` | **Hot path.** Equirectangular → perspective projection using a parallel worker pool. |
| `testpattern.go` | Synthetic checkerboard test pattern renderer (no panoramic image needed). |
| `encoder.go` | H.264 encoder wrapping an FFmpeg subprocess (libx264 ultrafast/zerolatency). |
| `jpeg.go` | RGB24 → JPEG encoding for the web MJPEG stream. Reusable `JPEGEncoder` avoids per-frame allocation. |
| `osd.go` | On-screen display: crosshair, timestamp, FPS, and PTZ info overlays. |
| `font.go` | Minimal 5×7 bitmap font for OSD text rendering. No external font dependencies. |

## Key Components

### PanoRenderer (`pano.go`)

The most CPU-intensive component. It renders a perspective view from a 360° equirectangular source image, simulating a real PTZ camera navigating a sphere.

**Per-frame pipeline:**
1. Convert PTZ state → yaw, pitch, focal length.
2. Precompute per-column yaw rotation products.
3. Dispatch row ranges to `runtime.NumCPU()` persistent worker goroutines.
4. Each worker renders its assigned rows: for every pixel, compute a 3D ray direction, apply pitch + yaw rotations, convert to spherical coordinates (θ, φ), map to equirectangular UV, and sample the source image with bilinear interpolation.
5. Draw OSD overlay.

**Performance optimisations:**
- **Precomputed column arrays:** `colCx[]` and `colCxSq[]` computed once at init.
- **Per-frame column products:** `colCxCosY[]` and `colCxSinY[]` avoid per-pixel multiplications.
- **Fixed-point 8.8 bilinear interpolation:** Integer multiply + shift instead of float ops.
- **Fast trig approximations:** `fastAtan2` and `fastAtanPos` use polynomial minimax approximations (~0.005 rad accuracy), replacing expensive `math.Atan2` calls in the inner loop.
- **Frame caching:** When the PTZ position hasn't changed, only the small OSD region is redrawn.
- **Persistent worker pool:** Avoids goroutine creation overhead per frame.

### Encoder (`encoder.go`)

Manages an FFmpeg subprocess for H.264 encoding:

- **Input:** Raw YUV420P frames piped to FFmpeg's stdin. RGB24→I420 conversion is done in-process using BT.601 coefficients with row-pair processing.
- **Encoding:** libx264 with `ultrafast` preset, `zerolatency` tune, `baseline` profile.
- **Level auto-selection:** Computes the minimum H.264 level from resolution × FPS to avoid "MB size exceeds level limit" warnings (e.g. 1920×1080@60fps requires level 4.2).
- **Output:** Annex B NAL units are parsed from stdout, grouped into access units (one per frame), and delivered via a buffered channel (capacity 4).
- **Resilience:** FFmpeg is automatically restarted on unexpected exit.
- **SPS/PPS extraction:** Captured from the first encoder output for RTSP SDP negotiation.

### RenderLoop (`renderloop.go`)

The main production loop coordinates frame timing:

- Runs at the configured FPS using `time.Ticker`.
- MJPEG snapshots are capped at ~15fps regardless of render FPS to save CPU.
- JPEG encoding is fully asynchronous — if the previous encode hasn't finished, the frame is skipped (non-blocking).
- FPS is measured over 1-second windows; stats are logged every 5 seconds.

### OSD (`osd.go` + `font.go`)

On-screen display elements drawn directly onto the RGB24 frame buffer:

- **DrawCrosshair:** White centre crosshair (2px thick, ~40px span). Used by `TestRenderer`.
- **DrawOSD:** Top-left info panel with darkened background showing timestamp, FPS, and PTZ position.
- **Bitmap font:** 5×7 glyphs stored as bit-packed byte arrays. Supports arbitrary integer scaling.
- **DrawTextShadow:** 1px black drop shadow for readability on any background.
- **DarkenRect:** Halves colour channels (right-shift by 1) for a translucent dark overlay.

### JPEGEncoder (`jpeg.go`)

Optimised JPEG encoding for the MJPEG web preview:

- Pre-allocates an RGBA pixel buffer with alpha channel pre-filled to 0xFF.
- On each `Encode()` call, only RGB bytes are copied (alpha stays 0xFF from init).
- Internal `bytes.Buffer` is reused via `Reset()`.
- Output is copied to a fresh slice so the internal buffer can be reused.

## Interfaces

```go
// Renderer produces raw RGB24 frames. Implemented by PanoRenderer and TestRenderer.
type Renderer interface {
    Render(pos ptz.Position, fps float64) []byte
}

// FrameSink receives JPEG snapshots for the web MJPEG stream.
type FrameSink interface {
    SetFrame(jpeg []byte)
}
```

## Data Flow

```
PTZ State ──► Renderer.Render() ──► RGB24 frame
                                        │
                    ┌───────────────────┤
                    ▼                    ▼
            Encoder.WriteFrame()   JPEGEncoder.Encode()
                    │                    │
            RGB24 → I420                 │
                    │                    ▼
            FFmpeg stdin            FrameSink.SetFrame()
                    │                    │
            H.264 stdout            MJPEG web stream
                    │
            readNALUs() parser
                    │
            AccessUnits channel
                    │
            RTSP StreamLoop
```

## Colour Space

- All renderers produce **RGB24** (3 bytes per pixel, packed).
- The encoder converts RGB24 → **I420 (YUV420P)** using BT.601 coefficients before feeding FFmpeg.
- JPEG encoding converts RGB24 → **RGBA** (with pre-filled alpha) for Go's `image/jpeg`.

## Configuration

The renderer is configured via parameters passed from `config.Config`:

| Parameter | Description | Default |
|-----------|-------------|---------|
| `WIDTH` | Output frame width | 1920 |
| `HEIGHT` | Output frame height | 1080 |
| `FPS` | Target frames per second | 30 |
| `BITRATE` | H.264 target bitrate | 2M |
| `PANO_PATH` | Path to equirectangular panoramic image | (required for PanoRenderer) |
