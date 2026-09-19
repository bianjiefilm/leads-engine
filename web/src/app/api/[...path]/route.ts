import { proxyToServer, readBffEnv } from "@/lib/bff";

// Catch-all BFF route: /api/* -> Go server /api/v1/*.
// e.g. /api/contacts -> /api/v1/contacts, /api/auth/login -> /api/v1/auth/login.

type Ctx = { params: Promise<{ path: string[] }> };

async function handle(req: Request, ctx: Ctx): Promise<Response> {
  const { path } = await ctx.params;
  return proxyToServer(req, readBffEnv(process.env), path);
}

export const GET = handle;
export const POST = handle;
export const PATCH = handle;
export const DELETE = handle;
