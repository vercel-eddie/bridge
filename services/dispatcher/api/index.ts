import type {VercelRequest, VercelResponse} from "@vercel/node";
import {waitUntil, getEnv} from "@vercel/functions";
import {getVercelOidcToken} from "@vercel/oidc";
import {handleRequest, type ResponseWriter} from "../src/common-handler.js";

/**
 * Adapts VercelResponse to ResponseWriter interface
 */
function adaptResponse(res: VercelResponse): ResponseWriter {
  return {
    status(code: number) {
      res.status(code);
      return this;
    },
    setHeader(name: string, value: string) {
      res.setHeader(name, value);
    },
    send(body: string) {
      res.send(body);
    },
    json(body: object) {
      res.json(body);
    },
  };
}

export default async function handler(req: VercelRequest, res: VercelResponse) {
  // Use Vercel SDK for system environment variables
  const env = getEnv();

  // Use Vercel OIDC SDK to get token
  let oidcToken = "";
  try {
    oidcToken = await getVercelOidcToken();
  } catch {
    console.log("OIDC token not available, continuing without it");
  }

  // Get body as string for POST requests
  let body: string | undefined;
  if (req.method === "POST" && req.body) {
    body = typeof req.body === "string" ? req.body : JSON.stringify(req.body);
  }

  await handleRequest(
    {method: req.method || "GET", url: req.url || "/", body},
    adaptResponse(res),
    {
      baseTunnelClientOptions: {
        deploymentId: env.VERCEL_DEPLOYMENT_ID,
        oidcToken,
        functionUrl: env.VERCEL_URL ? `https://${env.VERCEL_URL}` : undefined,
      },
      onBackgroundStart: (promise) => waitUntil(promise),
    }
  );
}
