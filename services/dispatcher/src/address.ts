import type { IncomingMessage } from "http";

export interface Address {
  ip: string;
  port: number;
}

export interface ConnectionAddresses {
  source: Address;
  dest: Address;
  connectionId: string;
}

/**
 * Extracts source (client) and destination (server) addresses from an HTTP request.
 * Works with both Node.js http.IncomingMessage and Vercel request objects.
 */
export function getConnectionAddresses(req: IncomingMessage): ConnectionAddresses {
  const source = getSourceAddress(req);
  const dest = getDestAddress(req);
  const connectionId = `${source.ip}:${source.port}-${dest.ip}:${dest.port}`;

  return { source, dest, connectionId };
}

/**
 * Extracts the client/source address from a request.
 */
function getSourceAddress(req: IncomingMessage): Address {
  return {
    ip: getClientIp(req),
    port: req.socket?.remotePort || 0,
  };
}

/**
 * Extracts the server/destination address from a request.
 */
function getDestAddress(req: IncomingMessage): Address {
  return {
    ip: getServerIp(req),
    port: getServerPort(req),
  };
}

/**
 * Gets the client IP from request headers or socket.
 */
function getClientIp(req: IncomingMessage): string {
  // Check x-forwarded-for header (set by proxies/load balancers)
  const forwarded = req.headers["x-forwarded-for"];
  if (forwarded) {
    const ips = Array.isArray(forwarded) ? forwarded[0] : forwarded;
    return ips.split(",")[0].trim();
  }

  // Check x-real-ip header (common alternative)
  const realIp = req.headers["x-real-ip"];
  if (realIp) {
    return Array.isArray(realIp) ? realIp[0] : realIp;
  }

  // Fall back to socket's remote address
  return req.socket?.remoteAddress || "127.0.0.1";
}

/**
 * Gets the server IP from request headers or socket.
 */
function getServerIp(req: IncomingMessage): string {
  // Check x-forwarded-host header (set by proxies)
  const forwardedHost = req.headers["x-forwarded-host"];
  if (forwardedHost) {
    const host = Array.isArray(forwardedHost) ? forwardedHost[0] : forwardedHost;
    return host.split(":")[0];
  }

  // Check host header
  const host = req.headers.host;
  if (host) {
    return host.split(":")[0];
  }

  // Fall back to socket's local address
  return req.socket?.localAddress || "127.0.0.1";
}

/**
 * Gets the server port from request headers or socket.
 */
function getServerPort(req: IncomingMessage): number {
  // Check x-forwarded-port header
  const forwardedPort = req.headers["x-forwarded-port"];
  if (forwardedPort) {
    const port = Array.isArray(forwardedPort) ? forwardedPort[0] : forwardedPort;
    const parsed = parseInt(port, 10);
    if (!isNaN(parsed)) return parsed;
  }

  // Check host header for port
  const host = req.headers.host;
  if (host && host.includes(":")) {
    const port = parseInt(host.split(":")[1], 10);
    if (!isNaN(port)) return port;
  }

  // Check x-forwarded-proto to infer default port
  const proto = req.headers["x-forwarded-proto"];
  if (proto) {
    const protocol = Array.isArray(proto) ? proto[0] : proto;
    if (protocol === "https") return 443;
    if (protocol === "http") return 80;
  }

  // Fall back to socket's local port
  return req.socket?.localPort || 0;
}
