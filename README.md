# Hikvision Doorbell Server

Go server providing WebRTC-based two-way audio and file playback for Hikvision doorbells.

## Features

- WebRTC bidirectional audio streaming
- HTTP endpoint for audio file playback
- Automatic session management
- Auto-discovery of available audio channels

## Requirements

- Hikvision doorbell with ISAPI two-way audio support
- Network access to doorbell
- ffmpeg (for CLI usage only)

## Installation

### Binary

```bash
make build
./doorbell-server -config config.yaml
```

### Container

```bash
# Pull from GitHub Container Registry
docker pull ghcr.io/acardace/hikvision-doorbell-server:latest

# Or build locally
docker build -t hikvision-doorbell-server -f Containerfile .
```

### Kubernetes

```bash
# Edit k8s/deployment.yaml with your doorbell credentials
kubectl apply -f k8s/deployment.yaml
```

## Configuration

Create `config.yaml`:

```yaml
server:
  host: "0.0.0.0"
  port: 8080

hikvision:
  host: "192.168.1.100"
  username: "admin"
  password: "your-password"
```

## CLI Usage

The CLI includes ffmpeg-based conversion for any audio format.

### Send Audio File
```bash
./doorbell-cli send -f message.mp3 -s http://localhost:8080
```

Converts any audio format to G.711 µ-law and plays on doorbell.

### Two-Way Audio
```bash
./doorbell-cli speak -s http://localhost:8080
```

Press Ctrl+C to stop.

## Integration

Designed for use with [Home Assistant integration](https://github.com/acardace/hikvision-doorbell-integration).

## Technical Details

- Audio codec: G.711 µ-law, 8000Hz, mono
- Protocol: Hikvision ISAPI over HTTP Digest Authentication
- WebRTC: Local network only (no STUN/TURN)
- Transport: RTP over HTTP

## Building

```bash
# Build binaries
make build

# Build server only
make build-server

# Build CLI only
make build-cli
```

## License

MIT
