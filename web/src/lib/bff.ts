// BFF proxy core. The web tier is a dumb, safe relay:
//   browser -> /api/* (this BFF) -> Go server /api/v1/* (loopback)
// Rules:
//   - the browser NEVER talks to the Go server or platform services directly
//   - the internal token is injected here and always overrides anything the
//     caller sends (no escalation)
//   - missing BFF config is a fail-closed 503; nothing is forwarded
//   - all authorization happens on the Go server; the BFF only relays verdicts

export interface BffEnv {
  serverUrl: string;
  internalToken: string;
}

export function readBffEnv(env: Record<string, string | undefined>): BffEnv {
  return {
    serverUrl: (env.LEADS_SERVER_URL ?? "").trim(),
    internalToken: (env.LEADS_INTERNAL_TOKEN ?? "").trim(),
  };
}

export function bffConfigProblems(env: BffEnv): string[] {
  const problems: string[] = [];
  if (!env.serverUrl) problems.push("LEADS_SERVER_URL is required");
  if (!env.internalToken) problems.push("LEADS_INTERNAL_TOKEN is required");
  return problems;
}

const HOP_BY_HOP = new Set([
  "connection", "keep-alive", "proxy-authenticate", "proxy-authorization",
  "te", "trailer", "transfer-encoding", "upgrade", "host",
]);

export function bffPathToUpstream(path: string[]): string {
  return "/api/v1/" + path.join("/");
}

export async function proxyToServer(
  req: Request,
  env: BffEnv,
  path: string[],
): Promise<Response> {
  const problems = bffConfigProblems(env);
  if (problems.length > 0) {
    return Response.json(
      {
        error: "bff_config_gate",
        message: "web BFF is not configured; refusing to forward (fail-closed)",
        detail: problems,
      },
      { status: 503 },
    );
  }

  const incoming = new URL(req.url);
  const upstreamUrl =
    env.serverUrl.replace(/\/+$/, "") +
    bffPathToUpstream(path) +
    incoming.search;

  const headers = new Headers();
  // forward the identity session cookie verbatim; never the caller's token
  const cookie = req.headers.get("cookie");
  if (cookie) headers.set("cookie", cookie);
  const contentType = req.headers.get("content-type");
  if (contentType) headers.set("content-type", contentType);
  // workspace selection is caller input; the Go server re-validates it
  // against membership (an unknown/foreign tenant can only ever yield 403)
  const tenant = req.headers.get("x-tenant-id");
  if (tenant) headers.set("x-tenant-id", tenant);
  headers.set("x-internal-token", env.internalToken);
  headers.set("x-forwarded-for-origin", incoming.origin);

  const init: RequestInit = {
    method: req.method,
    headers,
    redirect: "manual",
  };
  if (!["GET", "HEAD"].includes(req.method)) {
    init.body = await req.arrayBuffer();
  }

  let upstream: Response;
  try {
    upstream = await fetch(upstreamUrl, init);
  } catch {
    return Response.json(
      {
        error: "server_unavailable",
        message: "leads server could not be reached; refusing to fake a result",
      },
      { status: 503 },
    );
  }

  // relay status + safe headers (incl. every set-cookie)
  const outHeaders = new Headers();
  upstream.headers.forEach((value, key) => {
    if (!HOP_BY_HOP.has(key.toLowerCase())) outHeaders.append(key, value);
  });
  const body = await upstream.arrayBuffer();
  return new Response(body, {
    status: upstream.status,
    statusText: upstream.statusText,
    headers: outHeaders,
  });
}
