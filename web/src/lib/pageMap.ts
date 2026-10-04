// 已有用户页面到冻结页面模式的账本。只记账，不改界面。
// 14 个编号不能增删。每条声明必须带 primary_action、status、empty。
// 不引用外部页面模式包，不处理编辑器密度，也不实现公开页导航禁令。

export const PATTERN_IDS = [
  "portal",
  "login",
  "workspace",
  "list",
  "detail",
  "creation",
  "media_result",
  "compare",
  "review",
  "billing",
  "settings",
  "recovery",
  "public_consumer",
  "editor_shell",
] as const;

export type PatternId = (typeof PATTERN_IDS)[number];

export const REQUIRED_REGIONS = ["primary_action", "status", "empty"] as const;

const knownIDs = new Set<string>(PATTERN_IDS);

export interface PageDeclaration {
  id: string;
  regions: readonly string[];
}

export interface RouteEntry {
  path: string;
  id: string;
  regions: readonly string[];
}

export class UnknownPatternError extends Error {
  readonly id: string;

  constructor(id: string) {
    super(`unknown pattern id: ${id}`);
    this.name = "UnknownPatternError";
    this.id = id;
  }
}

export class MissingRegionError extends Error {
  readonly id: string;
  readonly regions: string[];

  constructor(id: string, regions: string[]) {
    const label = id === "" ? "<empty>" : id;
    super(`pattern ${label}: missing required region ${regions.join(",")}`);
    this.name = "MissingRegionError";
    this.id = id;
    this.regions = regions;
  }
}

export function patternIDs(): string[] {
  return [...PATTERN_IDS];
}

export function requiredRegions(): string[] {
  return [...REQUIRED_REGIONS];
}

export function catalog(): PageDeclaration[] {
  return PATTERN_IDS.map((id) => ({ id, regions: requiredRegions() }));
}

// 未知编号先失败。区域名逐字比较，前后空格和大小写都算不同。多余区域名忽略。
export function validateDeclaration(declaration: PageDeclaration): void {
  if (!knownIDs.has(declaration.id)) {
    throw new UnknownPatternError(declaration.id);
  }
  const have = new Set(declaration.regions);
  const missing = REQUIRED_REGIONS.filter((region) => !have.has(region));
  if (missing.length > 0) {
    throw new MissingRegionError(declaration.id, [...missing]);
  }
}

// 每条路由恰好一条。拿不准的编号在旁边写了理由。
const routes: readonly RouteEntry[] = [
  // 登录后的今天工作台，不是门户落地页。
  { path: "/", id: "workspace", regions: REQUIRED_REGIONS },
  { path: "/leads", id: "list", regions: REQUIRED_REGIONS },
  { path: "/leads/[id]", id: "detail", regions: REQUIRED_REGIONS },
  { path: "/contacts", id: "list", regions: REQUIRED_REGIONS },
  { path: "/contacts/[id]", id: "detail", regions: REQUIRED_REGIONS },
  { path: "/opportunities", id: "list", regions: REQUIRED_REGIONS },
  { path: "/opportunities/[id]", id: "detail", regions: REQUIRED_REGIONS },
  // 进行中的接待是记录列表；今天首页才是工作台，会话面板没有独立路由。
  { path: "/reception", id: "list", regions: REQUIRED_REGIONS },
  // 记录区是提醒和草稿列表；记下提醒不是独立向导。
  { path: "/sop", id: "list", regions: REQUIRED_REGIONS },
  // 主行动是记下一次模拟外呼，不是审阅已有成果，也不是外呼列表。
  { path: "/outbound", id: "creation", regions: REQUIRED_REGIONS },
  // 主行动是复算并阅读来源和费用结论，不是订阅账单，也不是并排改稿。
  { path: "/attribution", id: "review", regions: REQUIRED_REGIONS },
  { path: "/subscription", id: "billing", regions: REQUIRED_REGIONS },
  // 主行动是评估并人工确认分级，不是新建线索。
  { path: "/intent", id: "review", regions: REQUIRED_REGIONS },
  // 主界面是带行业、地区、规模筛选的企业表，导入确认不是独立路由。
  { path: "/enterprises", id: "list", regions: REQUIRED_REGIONS },
  // 授权互动以记录列表呈现，接入不是独立路由。
  { path: "/channel-interactions", id: "list", regions: REQUIRED_REGIONS },
  // 本租户隔离说明和导出，不是成员目录，也不是错误恢复页。
  { path: "/isolation", id: "settings", regions: REQUIRED_REGIONS },
  // 公开留资给顾客填写，不走后台壳，比门户更具体。
  { path: "/f/[id]", id: "public_consumer", regions: REQUIRED_REGIONS },
  // 访客 H5 不登录，不是员工接待台。
  { path: "/r/[id]", id: "public_consumer", regions: REQUIRED_REGIONS },
];

export function routeLedger(): RouteEntry[] {
  return routes.map((entry) => ({ path: entry.path, id: entry.id, regions: [...entry.regions] }));
}
