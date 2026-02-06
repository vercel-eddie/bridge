import { getTunnelClient, resetTunnelClient, getCurrentTunnelClient, type GetTunnelClientOptions } from "./connection.js";
import { fromJson } from "@bufbuild/protobuf";
import { ServerConnectionSchema, type ServerConnection } from "@vercel/bridge-api";

export interface RequestInfo {
  method: string;
  url: string;
  body?: string;
}

export interface ResponseWriter {
  status(code: number): ResponseWriter;
  setHeader(name: string, value: string): void;
  send(body: string): void;
  json(body: object): void;
}

export interface HandlerContext {
  /** Base options for tunnel client (deploymentId, oidcToken, functionUrl) */
  baseTunnelClientOptions?: Omit<GetTunnelClientOptions, "sandboxUrl">;
  /** Called when background processing should start (e.g., waitUntil) */
  onBackgroundStart?: (promise: Promise<void>) => void;
}

// Track if background processing has been started
let backgroundProcessingStarted = false;

// Default validation regex allows localhost and Vercel sandbox URLs
const DEFAULT_SANDBOX_URL_VALIDATION = "^https?://(localhost(:\\d+)?|sb-[a-z0-9]+\\.vercel\\.run)(/.*)?$";

/**
 * Validates the sandbox URL against the BRIDGE_SANDBOX_URL_VALIDATION regex.
 * Returns true if valid, false otherwise.
 */
function validateSandboxUrl(sandboxUrl: string): boolean {
  const validationRegex = process.env.BRIDGE_SANDBOX_URL_VALIDATION || DEFAULT_SANDBOX_URL_VALIDATION;

  try {
    const regex = new RegExp(validationRegex);
    return regex.test(sandboxUrl);
  } catch (error) {
    console.error("Invalid BRIDGE_SANDBOX_URL_VALIDATION regex:", error);
    return false;
  }
}

/**
 * Common handler for both local service and Vercel function.
 * Returns true if the request was handled, false if it should be passed through.
 */
export async function handleRequest(
  req: RequestInfo,
  res: ResponseWriter,
  ctx: HandlerContext = {}
): Promise<boolean> {
  console.log(`Incoming request: ${req.method} ${req.url}`);

  // Health check endpoint
  if (req.url === "/__bridge/health") {
    res.status(200).send("dispatcher ok");
    return true;
  }

  // Tunnel connect endpoint - receives ServerConnection from bridge server
  if (req.url === "/__tunnel/connect" && req.method === "POST") {
    await handleTunnelConnect(req, res, ctx);
    return true;
  }

  // Status endpoint - check if tunnel is connected
  if (req.url === "/__tunnel/status") {
    const client = getCurrentTunnelClient();
    if (client && client.connected) {
      res.status(200).json({
        status: "connected",
        message: "Dispatcher tunnel active",
      });
    } else {
      res.status(200).json({
        status: "disconnected",
        message: "No active tunnel connection",
      });
    }
    return true;
  }

  // Default: return 404 for unknown routes
  res.status(404).json({
    error: "Not Found",
    message: "Unknown endpoint. Use /__tunnel/connect to establish tunnel.",
  });
  return true;
}

async function handleTunnelConnect(
  req: RequestInfo,
  res: ResponseWriter,
  ctx: HandlerContext
): Promise<void> {
  // Parse ServerConnection from request body
  if (!req.body) {
    res.status(400).json({
      status: "error",
      error: "Missing request body",
      details: "Expected ServerConnection JSON payload",
    });
    return;
  }

  let serverConnection: ServerConnection;
  try {
    const jsonData = JSON.parse(req.body);
    serverConnection = fromJson(ServerConnectionSchema, jsonData);
  } catch (error) {
    console.error("Failed to parse ServerConnection:", error);
    res.status(400).json({
      status: "error",
      error: "Invalid request body",
      details: error instanceof Error ? error.message : String(error),
    });
    return;
  }

  const { sandboxUrl } = serverConnection;

  // Validate sandbox URL against regex
  if (!validateSandboxUrl(sandboxUrl)) {
    console.error(`Sandbox URL validation failed: ${sandboxUrl}`);
    res.status(403).json({
      status: "error",
      error: "Sandbox URL validation failed",
      details: "The sandbox_url does not match the allowed pattern",
    });
    return;
  }

  console.log(`Validated sandbox URL: ${sandboxUrl}`);

  try {
    const clientOptions: GetTunnelClientOptions = {
      sandboxUrl,
      ...ctx.baseTunnelClientOptions,
    };

    const client = await getTunnelClient(clientOptions);

    // Start background processing only once per connection
    if (!backgroundProcessingStarted && ctx.onBackgroundStart) {
      backgroundProcessingStarted = true;

      const backgroundPromise = client.runUntilDone().catch((error) => {
        console.error("Tunnel processing error:", error);
        resetTunnelClient();
        backgroundProcessingStarted = false;
      });

      ctx.onBackgroundStart(backgroundPromise);
    }

    // Wait briefly to catch immediate connection failures
    await new Promise((resolve) => setTimeout(resolve, 100));

    // Verify the client is still connected
    if (!client.connected) {
      resetTunnelClient();
      backgroundProcessingStarted = false;
      res.status(503).json({
        status: "error",
        error: "Failed to establish tunnel connection",
        details: "Connection was closed immediately after connecting",
        sandboxUrl,
      });
      return;
    }

    res.status(200).json({
      status: "connected",
      message: "Tunnel connection established",
      sandboxUrl,
    });
  } catch (error) {
    console.error("Tunnel connect error:", error);
    resetTunnelClient();
    backgroundProcessingStarted = false;

    res.status(503).json({
      status: "error",
      error: "Unable to connect to sandbox",
      details: error instanceof Error ? error.message : String(error),
      sandboxUrl,
    });
  }
}

/**
 * Reset the background processing state (for testing or reconnection)
 */
export function resetBackgroundState(): void {
  backgroundProcessingStarted = false;
}
