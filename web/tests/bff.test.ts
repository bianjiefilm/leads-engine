// BFF tests: the web relay must be a faithful, non-escalating proxy.
// The role x assignment x tenant matrix is enforced by the Go server; these
// tests prove the BFF (1) forwards identity/cookies verbatim, (2) injects and
// overrides the internal token, (3) relays every verdict unchanged (no status
// widening, no silent success), (4) fails closed when unconfigured.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { bffPathToUpstream, proxyToServer, readBffEnv, type BffEnv } from "../src/lib/bff";

const ENV: BffEnv = { serverUrl: "http://127.0.0.1:18230", internalToken: "bff-internal-secret" };

// ---- fake Go server implementing the same matrix the integration tests cover ----

const MEMBER_IDS = {
  ownerA: "mem_owner_a",
  salesA1: "mem_sales_a1",
  salesA2: "mem_sales_a2",
  agentA: "mem_agent_a",
};

interface Session {
  principal: string;
  tenant: string;
  role: "owner" | "sales" | "agent";
  enabled: boolean;
  grant: boolean;
}

const SESSIONS: Record<string, Session> = {
  "sess-owner-a": { principal: "usr_owner_a", tenant: "tnt_A", role: "owner", enabled: true, grant: false },
  "sess-sales-a1": { principal: "usr_sales_a1", tenant: "tnt_A", role: "sales", enabled: true, grant: false },
  "sess-sales-a2": { principal: "usr_sales_a2", tenant: "tnt_A", role: "sales", enabled: true, grant: false },
  "sess-agent-a": { principal: "usr_agent_a", tenant: "tnt_A", role: "agent", enabled: true, grant: false },
  "sess-agent-a-granted": { principal: "usr_agent_a", tenant: "tnt_A", role: "agent", enabled: true, grant: true },
  "sess-disabled-a": { principal: "usr_disabled_a", tenant: "tnt_A", role: "sales", enabled: false, grant: false },
  "sess-owner-b": { principal: "usr_owner_b", tenant: "tnt_B", role: "owner", enabled: true, grant: false },
};

// contactA1 lives in tenant A, assigned to salesA1.
const RECORDS: Record<string, { tenant: string; assignee: string }> = {
  con_A1: { tenant: "tnt_A", assignee: MEMBER_IDS.salesA1 },
};

function upstreamMatrix(req: Request, path: string): Response {
  const url = new URL(req.url);
  const fail = (status: number, code: string) =>
    new Response(JSON.stringify({ error: code }), { status });

  if (req.headers.get("x-internal-token") !== ENV.internalToken) {
    return fail(401, "unauthorized");
  }
  const sessionToken = cookieToken(req.headers.get("cookie"));
  const sess = sessionToken ? SESSIONS[sessionToken] : undefined;
  if (!sess) return fail(401, "unauthenticated");
  if (!sess.enabled) return fail(403, "member_disabled");

  const tenant = req.headers.get("x-tenant-id") ?? "";
  if (!tenant) return fail(400, "tenant_required");
  if (sess.role === "agent" && !sess.grant) return fail(403, "agent_grant_missing");
  if (tenant !== sess.tenant) return fail(403, "cross_tenant");

  if (path === "contacts" && req.method === "GET") {
    // owner sees all; sales/agent see only their own (server filters)
    return Response.json({ items: sess.role === "owner" ? ["all"] : ["own"] });
  }
  if (path === "contacts" && req.method === "POST") {
    return Response.json({ id: "con_new" }, { status: 201 });
  }
  const match = path.match(/^contacts\/([^/]+)$/);
  if (match) {
    const rec = RECORDS[match[1]];
    if (!rec || rec.tenant !== sess.tenant) return fail(404, "not_found");
    if (sess.role === "owner") {
      if (req.method === "GET") return Response.json({ id: match[1] });
      return Response.json({ id: match[1], patched: true });
    }
    if (rec.assignee === MEMBER_IDS[sess.principal === "usr_agent_a" ? "agentA" : sess.principal === "usr_sales_a1" ? "salesA1" : "salesA2"]) {
      if (req.method === "GET") return Response.json({ id: match[1] });
      return Response.json({ id: match[1], patched: true });
    }
    // not yours: masked as 404, never widened by the BFF
    return fail(404, "not_found");
  }
  if (path === "admin/members") {
    if (sess.role !== "owner") return fail(403, "forbidden");
    return Response.json({ items: [] });
  }
  if (path === "admin/export") {
    if (sess.role !== "owner") return fail(403, "forbidden");
    return Response.json({ contacts: [], leads: [], opportunities: [] });
  }
  return fail(404, "no_route");
}

function cookieToken(cookie: string | null): string | null {
  if (!cookie) return null;
  const m = cookie.match(/leads_session=([^;]+)/);
  return m ? m[1] : null;
}

let upstreamCalls: { url: string; headers: Headers; method: string }[] = [];

beforeEach(() => {
  upstreamCalls = [];
  vi.stubGlobal("fetch", vi.fn(async (input: string | URL | Request, init?: RequestInit) => {
    const url = String(input);
    upstreamCalls.push({
      url,
      headers: new Headers(init?.headers as HeadersInit),
      method: init?.method ?? "GET",
    });
    const parsed = new URL(url);
    const path = parsed.pathname.replace(/^\/api\/v1\//, "");
    const req = new Request(url, {
      method: init?.method ?? "GET",
      headers: init?.headers as HeadersInit,
      body: init?.body as BodyInit,
    });
    return upstreamMatrix(req, path);
  }));
});
afterEach(() => vi.unstubAllGlobals());

function bffReq(method: string, path: string, opts: { cookie?: string; tenant?: string; body?: string; extraHeaders?: Record<string, string> } = {}): Request {
  const headers = new Headers(opts.extraHeaders);
  if (opts.cookie) headers.set("cookie", opts.cookie);
  if (opts.tenant) headers.set("x-tenant-id", opts.tenant);
  if (opts.body) headers.set("content-type", "application/json");
  return new Request(`http://bff.local/api/${path}`, { method, headers, body: opts.body });
}

const segments = (p: string) => p.split("/").filter(Boolean);

async function call(method: string, path: string, opts: Parameters<typeof bffReq>[2] = {}) {
  return proxyToServer(bffReq(method, path, opts), ENV, segments(path));
}

// ---- config gate ------------------------------------------------------------------

describe("BFF config gate", () => {
  it("fails closed with 503 and never calls upstream when unconfigured", async () => {
    const res = await proxyToServer(bffReq("GET", "contacts", { tenant: "tnt_A" }), { serverUrl: "", internalToken: "" }, ["contacts"]);
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("bff_config_gate");
    expect(body.detail).toEqual(["LEADS_SERVER_URL is required", "LEADS_INTERNAL_TOKEN is required"]);
    expect(upstreamCalls).toHaveLength(0);
  });

  it("readBffEnv trims and reads the documented keys", () => {
    const env = readBffEnv({ LEADS_SERVER_URL: " http://x ", LEADS_INTERNAL_TOKEN: " t " });
    expect(env.serverUrl).toBe("http://x");
    expect(env.internalToken).toBe("t");
  });
});

// ---- the role x assignment x tenant matrix, through the BFF ------------------------

describe("BFF matrix relay (role x assignment x tenant)", () => {
  const cookie = (s: string) => `leads_session=${s}; HttpOnly`;

  const matrix: {
    name: string;
    session: string;
    tenant: string;
    method: string;
    path: string;
    want: number;
  }[] = [
    { name: "owner reads tenant list", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "contacts", want: 200 },
    { name: "assigned sales reads own record", session: "sess-sales-a1", tenant: "tnt_A", method: "GET", path: "contacts/con_A1", want: 200 },
    { name: "unassigned sales blocked (404 mask)", session: "sess-sales-a2", tenant: "tnt_A", method: "GET", path: "contacts/con_A1", want: 404 },
    { name: "sales cannot manage members", session: "sess-sales-a1", tenant: "tnt_A", method: "GET", path: "admin/members", want: 403 },
    { name: "sales cannot export", session: "sess-sales-a1", tenant: "tnt_A", method: "GET", path: "admin/export", want: 403 },
    { name: "owner exports", session: "sess-owner-a", tenant: "tnt_A", method: "GET", path: "admin/export", want: 200 },
    { name: "cross tenant read refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "contacts/con_A1", want: 403 },
    { name: "cross tenant list refused", session: "sess-owner-b", tenant: "tnt_A", method: "GET", path: "contacts", want: 403 },
    { name: "agent without grant refused", session: "sess-agent-a", tenant: "tnt_A", method: "GET", path: "contacts", want: 403 },
    { name: "agent with per-tenant grant allowed", session: "sess-agent-a-granted", tenant: "tnt_A", method: "GET", path: "contacts", want: 200 },
    { name: "disabled member refused", session: "sess-disabled-a", tenant: "tnt_A", method: "GET", path: "contacts", want: 403 },
    { name: "owner create contact", session: "sess-owner-a", tenant: "tnt_A", method: "POST", path: "contacts", want: 201 },
    { name: "sales create contact", session: "sess-sales-a1", tenant: "tnt_A", method: "POST", path: "contacts", want: 201 },
    { name: "owner patch any record", session: "sess-owner-a", tenant: "tnt_A", method: "PATCH", path: "contacts/con_A1", want: 200 },
    { name: "assigned sales patch own record", session: "sess-sales-a1", tenant: "tnt_A", method: "PATCH", path: "contacts/con_A1", want: 200 },
    { name: "unassigned sales patch others blocked", session: "sess-sales-a2", tenant: "tnt_A", method: "PATCH", path: "contacts/con_A1", want: 404 },
  ];

  for (const c of matrix) {
    it(c.name, async () => {
      const res = await call(c.method, c.path, {
        cookie: cookie(c.session),
        tenant: c.tenant,
        body: c.method === "GET" ? undefined : "{}",
      });
      expect(res.status).toBe(c.want);
    });
  }
});

// ---- relay semantics ---------------------------------------------------------------

describe("BFF relay semantics", () => {
  it("maps /api/X to upstream /api/v1/X and preserves the query string", async () => {
    const req = new Request("http://bff.local/api/contacts?limit=5&sort=name");
    await proxyToServer(req, ENV, ["contacts"]);
    // query must come from the original request
    expect(upstreamCalls[0].url).toContain("http://127.0.0.1:18230/api/v1/contacts?limit=5&sort=name");
  });

  it("forwards the session cookie verbatim", async () => {
    await call("GET", "contacts", { cookie: "leads_session=sess-owner-a", tenant: "tnt_A" });
    expect(upstreamCalls[0].headers.get("cookie")).toBe("leads_session=sess-owner-a");
  });

  it("overrides any caller-supplied internal token (no escalation)", async () => {
    const res = await call("GET", "contacts", {
      cookie: "leads_session=sess-owner-a",
      tenant: "tnt_A",
      extraHeaders: { "x-internal-token": "forged-token" },
    });
    expect(res.status).toBe(200);
    expect(upstreamCalls[0].headers.get("x-internal-token")).toBe(ENV.internalToken);
  });

  it("relays upstream errors unchanged (no silent success)", async () => {
    const res = await call("GET", "contacts", { cookie: "leads_session=sess-disabled-a", tenant: "tnt_A" });
    expect(res.status).toBe(403);
    const body = await res.json();
    expect(body.error).toBe("member_disabled");
  });

  it("relays set-cookie headers back to the browser (login flow)", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => {
      const res = new Response(JSON.stringify({ authenticated: true }), { status: 200 });
      res.headers.append("set-cookie", "leads_session=tok1; HttpOnly; SameSite=Lax");
      res.headers.append("set-cookie", "leads_session_refresh=rt1; HttpOnly; SameSite=Lax");
      return res;
    }));
    const res = await call("POST", "auth/login", { body: JSON.stringify({ email: "a@b.c", password: "x" }) });
    expect(res.status).toBe(200);
    const setCookies = res.headers.getSetCookie();
    expect(setCookies).toHaveLength(2);
    expect(setCookies[0]).toContain("leads_session=tok1");
  });

  it("when the Go server is down the BFF answers 503 server_unavailable", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => { throw new Error("ECONNREFUSED"); }));
    const res = await call("GET", "contacts", { cookie: "leads_session=sess-owner-a", tenant: "tnt_A" });
    expect(res.status).toBe(503);
    const body = await res.json();
    expect(body.error).toBe("server_unavailable");
  });

  it("bffPathToUpstream builds /api/v1/... paths", () => {
    expect(bffPathToUpstream(["contacts", "con_1"])).toBe("/api/v1/contacts/con_1");
    expect(bffPathToUpstream(["auth", "login"])).toBe("/api/v1/auth/login");
  });
});
