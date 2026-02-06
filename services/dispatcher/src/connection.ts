import { TunnelClient, type TunnelConfig } from "./tunnel.js";

// Singleton tunnel client state
let tunnelClient: TunnelClient | null = null;
let connectionPromise: Promise<void> | null = null;
let currentSandboxUrl: string | null = null;

// Connection timeout in milliseconds
const CONNECTION_TIMEOUT_MS = 10000;

/**
 * Wraps a promise with a timeout.
 */
function withTimeout<T>(promise: Promise<T>, ms: number, message: string): Promise<T> {
  const timeout = new Promise<never>((_, reject) => {
    setTimeout(() => reject(new Error(message)), ms);
  });
  return Promise.race([promise, timeout]);
}

export interface GetTunnelClientOptions {
  /** The sandbox URL to connect to (from ServerConnection) */
  sandboxUrl: string;
  /** The connection key for tunnel pairing (from ServerConnection) */
  connectionKey: string;
  /** Override function URL (uses VERCEL_URL or FUNCTION_URL env var if not provided) */
  functionUrl?: string;
}

/**
 * Gets or creates the singleton tunnel client connection.
 * Reuses existing connection if available, otherwise establishes a new one.
 */
export async function getTunnelClient(options: GetTunnelClientOptions): Promise<TunnelClient> {
  const { sandboxUrl } = options;

  // Return existing client if connected and still alive and same sandbox
  if (tunnelClient && tunnelClient.connected && currentSandboxUrl === sandboxUrl) {
    return tunnelClient;
  }

  // Reset stale client or different sandbox
  if (tunnelClient && (!tunnelClient.connected || currentSandboxUrl !== sandboxUrl)) {
    console.log("Tunnel client disconnected or sandbox changed, reconnecting...");
    tunnelClient.disconnect();
    tunnelClient = null;
    connectionPromise = null;
    currentSandboxUrl = null;
  }

  // If already connecting, wait for that to complete
  if (connectionPromise) {
    await connectionPromise;
    return tunnelClient!;
  }

  const config: TunnelConfig = {
    sandboxUrl,
    connectionKey: options.connectionKey,
    functionUrl: options.functionUrl
      || (process.env.VERCEL_URL ? `https://${process.env.VERCEL_URL}` : null)
      || process.env.FUNCTION_URL
      || "http://localhost:8080",
  };

  // Create tunnel client
  tunnelClient = new TunnelClient(config);
  currentSandboxUrl = sandboxUrl;

  // Connect to sandbox with timeout
  connectionPromise = withTimeout(
    tunnelClient.connect(),
    CONNECTION_TIMEOUT_MS,
    `Connection to sandbox at ${sandboxUrl} timed out after ${CONNECTION_TIMEOUT_MS}ms`
  );

  try {
    await connectionPromise;
    console.log(`Connected to sandbox at ${sandboxUrl}`);
  } catch (error) {
    // Reset state on connection failure
    tunnelClient = null;
    connectionPromise = null;
    currentSandboxUrl = null;
    throw error;
  }

  return tunnelClient;
}

/**
 * Resets the tunnel client state. Call this on connection errors
 * to allow reconnection on the next request.
 */
export function resetTunnelClient(): void {
  tunnelClient = null;
  connectionPromise = null;
}

/**
 * Returns the current tunnel client if connected, or null if not.
 */
export function getCurrentTunnelClient(): TunnelClient | null {
  return tunnelClient;
}
