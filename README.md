# Hikvision Doorbell Server

Go server providing WebRTC-based two-way audio and file playback for Hikvision doorbells.

## Features

- WebRTC bidirectional audio streaming
- HTTP endpoint for audio file playback
- Automatic session management
- Auto-discovery of available audio channels

## Requirements

- Hikvision doorbell with ISAPI two-way audio support
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

Create a ConfigMap for the server configuration:

```yaml
# configmap.yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: doorbell-config
  namespace: apps
data:
  config.yaml: |
    server:
      host: "0.0.0.0"
      port: 8080
    hikvision:
      host: "192.168.1.100"
      username: "admin"
      password: "your-password"
```

Create the Deployment:

```yaml
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: hikvision-doorbell-server
  namespace: apps
spec:
  replicas: 1
  selector:
    matchLabels:
      app: hikvision-doorbell
  template:
    metadata:
      labels:
        app: hikvision-doorbell
    spec:
      containers:
      - name: server
        image: ghcr.io/acardace/hikvision-doorbell-server:latest
        ports:
        - containerPort: 8080
          name: http
          protocol: TCP
        - containerPort: 50000
          name: webrtc
          protocol: UDP
        env:
        - name: WEBRTC_PUBLIC_IP
          value: "203.0.113.10"  # Your public IP for WebRTC
        volumeMounts:
        - name: config
          mountPath: /app/config.yaml
          subPath: config.yaml
      volumes:
      - name: config
        configMap:
          name: doorbell-config
```

Create a Service for HTTP/HTTPS traffic:

```yaml
# service.yaml
apiVersion: v1
kind: Service
metadata:
  name: hikvision-doorbell-server
  namespace: apps
spec:
  selector:
    app: hikvision-doorbell
  ports:
  - port: 8080
    targetPort: 8080
    name: http
```

Create a LoadBalancer Service for WebRTC UDP traffic:

```yaml
# service-webrtc.yaml
apiVersion: v1
kind: Service
metadata:
  name: hikvision-doorbell-webrtc
  namespace: apps
spec:
  type: LoadBalancer
  selector:
    app: hikvision-doorbell
  ports:
  - port: 50000
    targetPort: 50000
    protocol: UDP
    name: webrtc
```

Create an HTTPRoute (for Gateway API) or Ingress for HTTPS:

```yaml
# httproute.yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: hikvision-doorbell
  namespace: apps
spec:
  parentRefs:
  - name: gateway
    namespace: infrastructure
  hostnames:
  - doorbell-server.example.com
  rules:
  - matches:
    - path:
        type: PathPrefix
        value: /
    backendRefs:
    - name: hikvision-doorbell-server
      port: 8080
```

Apply the manifests:

```bash
kubectl apply -f configmap.yaml
kubectl apply -f deployment.yaml
kubectl apply -f service.yaml
kubectl apply -f service-webrtc.yaml
kubectl apply -f httproute.yaml
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

Apache License 2.0
