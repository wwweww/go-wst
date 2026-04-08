# go-wst Examples

This directory contains example applications demonstrating how to use the go-wst library.

## Examples

### 1. Chat - WebSocket (Stateful)

A simple chat room application demonstrating **stateful** WebSocket connections.

```bash
cd chat
go run main.go -f etc/chat.yaml
```

- URL: `ws://localhost:9001/ws`

### 2. Chat - WebTransport

A chat room using **WebTransport** (requires TLS).

```bash
cd chat-wt
go run main.go -f etc/chat-wt.yaml
```

- URL: `https://localhost:9003/wt`

TLS certificate can be loaded via file paths or environment variables (see `etc/chat-wt.yaml`).

### 3. Chat - Hybrid (WebSocket + WebTransport)

A hybrid server supporting both protocols with automatic fallback.

```bash
cd chat-hybrid
go run main.go -f etc/chat-hybrid.yaml
```

- WebSocket: `ws://localhost:9004/ws`
- WebTransport: `https://localhost:9005/wt`

### 4. Notification (Stateless)

A notification system demonstrating **stateless** message polling.

```bash
cd notification
go run main.go -f etc/notification.yaml
```

- URL: `ws://localhost:9002/ws`

---

## Message Formats (Chat examples)

1. **Join room:**
```json
{
    "type": "join",
    "roomId": "room1",
    "userId": "user1"
}
```

2. **Send message:**
```json
{
    "type": "message",
    "roomId": "room1",
    "userId": "user1",
    "content": "Hello everyone!"
}
```

3. **Leave room:**
```json
{
    "type": "leave",
    "roomId": "room1",
    "userId": "user1"
}
```

---

## TLS Configuration (WebTransport)

WebTransport requires HTTPS. TLS certificates can be provided in two ways:

### Option 1: File paths (local dev / K8s volume mount)

```yaml
CertFile: certs/cert.pem
KeyFile: certs/key.pem
```

Generate self-signed certs for development:
```bash
openssl req -x509 -newkey rsa:2048 -keyout key.pem -out cert.pem -days 365 -nodes
```

### Option 2: Environment variables (K8s Secret env injection)

```yaml
CertEnvVar: WT_TLS_CERT
KeyEnvVar: WT_TLS_KEY
```

K8s Deployment:
```yaml
env:
  - name: WT_TLS_CERT
    valueFrom:
      secretKeyRef:
        name: my-tls-secret
        key: tls.crt
  - name: WT_TLS_KEY
    valueFrom:
      secretKeyRef:
        name: my-tls-secret
        key: tls.key
```

Priority: `CertEnvVar/KeyEnvVar` > `CertFile/KeyFile`

---

## Testing Tools

### Browser-based WebSocket Client

Use the test client at `test-client/index.html` for quick testing.

### Python Test Script

```python
import asyncio
import websockets
import json

async def test_chat():
    uri = "ws://localhost:9001/ws"
    async with websockets.connect(uri) as ws:
        # Join room
        await ws.send(json.dumps({
            "type": "join",
            "roomId": "room1",
            "userId": "test-user"
        }))
        
        response = await ws.recv()
        print(f"Received: {response}")
        
        # Send message
        await ws.send(json.dumps({
            "type": "message",
            "roomId": "room1",
            "userId": "test-user",
            "content": "Hello from Python!"
        }))

asyncio.run(test_chat())
```
