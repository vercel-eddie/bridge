# Architecture

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                             Developer Machine                                │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                              Devcontainer                              │  │
│  │  ┌─────────────┐    ┌──────────────────┐    ┌───────────────────────┐  │  │
│  │  │ Local Code  │───▶│ bridge intercept │───▶│    Local TCP Proxy    │  │  │
│  │  │  Workspace  │    │   (CLI command)  │    │      (SSH port)       │  │  │
│  │  └──────┬──────┘    └──────────────────┘    └───────────┬───────────┘  │  │
│  │         │                                               │              │  │
│  │         │ file sync                                     │              │  │
│  │         │ (mutagen)                                     │              │  │
│  │         │                                               │              │  │
│  └─────────┼───────────────────────────────────────────────┼──────────────┘  │
│            │                                               │                 │
└────────────┼───────────────────────────────────────────────┼─────────────────┘
             │                                               │
             │ SSH over WebSocket (/ssh)                     │ WebSocket
             │                                               │
             ▼                                               ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              Vercel Sandbox                                  │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                            bridge server                               │  │
│  │  ┌───────────────┐    ┌──────────────────┐    ┌─────────────────────┐  │  │
│  │  │  SSH Server   │◀──▶│ WebSocket Server │◀──▶│    HTTP CONNECT     │  │  │
│  │  │  (port 2222)  │    │  /health, /ssh   │    │       Proxy         │  │  │
│  │  └───────────────┘    │     /tunnel      │    └─────────────────────┘  │  │
│  │                       └─────────┬────────┘                             │  │
│  └─────────────────────────────────┼──────────────────────────────────────┘  │
│          ▲                         │                                         │
│          │ file sync (mutagen)     │ WebSocket tunnel                        │
│          │                         ▼                                         │
│  ┌───────┴───────┐                                                           │
│  │    Sandbox    │                                                           │
│  │   Workspace   │                                                           │
│  └───────────────┘                                                           │
└──────────────────────────────────────────────────────────────────────────────┘
                                     │
                                     │ WebSocket
                                     ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                         Vercel Preview Deployment                            │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│  │                          Dispatcher Service                            │  │
│  │  ┌─────────────────┐    ┌─────────────────┐    ┌────────────────────┐  │  │
│  │  │  Incoming HTTP  │───▶│  Tunnel Client  │───▶│      Forward       │  │  │
│  │  │    Requests     │    │   (Singleton)   │    │      Response      │  │  │
│  │  └─────────────────┘    └─────────────────┘    └────────────────────┘  │  │
│  └────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
                                     ▲
                                     │
                                     │ HTTPS
                                     │
                            ┌────────┴────────┐
                            │   End Users /   │
                            │   API Clients   │
                            └─────────────────┘
```

### Component Overview

| Component         | Description                                                  |
|-------------------|--------------------------------------------------------------|
| **bridge CLI**    | Go CLI with `create`, `connect`, and `server` commands       |
| **Devcontainer**  | Local development container with `bridge intercept` injected |
| **bridge server** | SSH + WebSocket server running in the Sandbox                |
| **Dispatcher**    | Node.js service on Preview deployment that tunnels requests  |
| **Mutagen**       | File synchronization between local and Sandbox workspaces    |

### Data Flow

1. **File Sync**: Local code ←→ Sandbox workspace via SSH + Mutagen
2. **SSH Access**: Developer SSH → Local TCP Proxy → WebSocket → Sandbox SSH Server
3. **Request Forwarding**: End User → Preview Deployment → Dispatcher → Tunnel → Sandbox → Local Dev

## Order of operations

1. User runs `vc connect --dev`
2. `vc` spins up a Preview deployment using the same env vars as your current project but is actually running
   the [dispatcher service](services/dispatcher).
3. `vc` spins up a Sandbox for the development session.
4. `vc` spins up a Devcontainer locally but with the `bridge` CLI injected as a Devcontainer feature.
5. `bridge intercept` is run in the Devcontainer feature which:

Connects to the `bridge` server on the Sandbox:

* SSH: Connects to the `bridge` server on `/ssh` and starts a local TCP proxy to allow for SSH
  connections within the container
* Tunnel: Forwards all intercepted egress L4 traffic down the websocket created on the `/tunnel` endpoint of the bridge
  server
  * `bridge` server then forwards all traffic to the Dispatcher preview deployment who then makes the call.

Starts [mutagen](https://mutagen.io/) to sync the Devcontainer workspace to the Sandbox workspace and vice versa via the
above SSH connection to the Sandbox.

## Tunnel Protocol

The `/tunnel` WebSocket endpoint on the bridge server enables bidirectional communication between the local client
(in the Devcontainer) and the dispatcher (on the Preview deployment). The bridge server acts as a relay, pairing
client and server connections.

### Connection Flow

```
┌─────────────┐                    ┌───────────────┐                    ┌────────────┐
│   Client    │                    │ Bridge Server │                    │ Dispatcher │
│ (Devcontainer)                   │   (Sandbox)   │                    │ (Preview)  │
└──────┬──────┘                    └───────┬───────┘                    └─────┬──────┘
       │                                   │                                  │
       │ 1. WebSocket connect /tunnel      │                                  │
       │──────────────────────────────────▶│                                  │
       │                                   │                                  │
       │ 2. Send registration              │                                  │
       │    {type: "client",               │                                  │
       │     function_url: "..."}          │                                  │
       │──────────────────────────────────▶│                                  │
       │                                   │                                  │
       │                                   │ 3. POST /__tunnel/connect        │
       │                                   │    Body: ServerConnection (JSON) │
       │                                   │─────────────────────────────────▶│
       │                                   │                                  │
       │                                   │        4. Validate function_url  │
       │                                   │           (must match regexp)    │
       │                                   │                                  │
       │                                   │ 5. WebSocket connect /tunnel     │
       │                                   │◀─────────────────────────────────│
       │                                   │                                  │
       │                                   │ 6. Send registration             │
       │                                   │    {type: "server"}              │
       │                                   │◀─────────────────────────────────│
       │                                   │                                  │
       │ 7. Bridge pipes connections       │                                  │
       │◀─────────────────────────────────▶│◀────────────────────────────────▶│
       │                                   │                                  │
```

### Bridge Server Implementation

1. Accept WebSocket connection on `/tunnel`
2. Wait for registration message (30s timeout)
3. If registration is `type: "client"`:

- Extract `function_url` from registration
- POST to `{function_url}/__tunnel/connect` with `ServerConnection` protobuf as JSON payload
- Hold connection waiting for matching server connection

4. If registration is `type: "server"`:

- Match with waiting client connection
- Begin piping data bidirectionally between client and server

5. On timeout or error, clean up connection
