import {
  isRenderable,
  parseEcoNavDocument,
  pinLeadsApp,
  REGISTRY_WITHOUT_IDENTITY_SUMMARY,
  toViewModel,
  type EcoNavModel,
  type EcoNavProvenance,
} from "@/lib/eco-nav/model";
import { provisionalEcoNav } from "@/lib/eco-nav/fixture";

export { provisionalEcoNav };

export interface LoadEcoNavInput {
  endpoint: string | null;
  identityEnabled: boolean;
  fetchImpl?: typeof fetch;
}

/**
 * 读取生态导航。没有目录地址、网络失败或契约不符时回到预览视图。
 * 解析成功但不可渲染（hidden / denied / unavailable）时不换成预览夹具。
 * 只有解析成功且身份开关打开，才标成 public-ai 上下文。
 * 不附带其它 App 的令牌。
 */
export async function loadEcoNavModel(input: LoadEcoNavInput): Promise<EcoNavModel> {
  const fixture = pinLeadsApp(provisionalEcoNav());
  if (!input.endpoint) return fixture;

  const fetchImpl = input.fetchImpl ?? fetch;
  let response: Response;
  try {
    response = await fetchImpl(input.endpoint, {
      method: "GET",
      headers: { accept: "application/json" },
      cache: "no-store",
    });
  } catch {
    return provisionalEcoNav("导航目录暂不可用，正在使用预览上下文");
  }
  if (!response.ok) {
    return provisionalEcoNav("导航目录暂不可用，正在使用预览上下文");
  }

  let json: unknown;
  try {
    json = await response.json();
  } catch {
    return provisionalEcoNav("导航契约不兼容，正在使用预览上下文");
  }
  const parsed = parseEcoNavDocument(json);
  if (!parsed.ok) {
    return provisionalEcoNav("导航契约不兼容，正在使用预览上下文");
  }

  const provenance: EcoNavProvenance = input.identityEnabled ? "public_ai_context" : "provisional_fixture";
  const statusSummary =
    !input.identityEnabled && isRenderable(parsed.document) ? REGISTRY_WITHOUT_IDENTITY_SUMMARY : "";
  return pinLeadsApp(toViewModel(parsed.document, { provenance, statusSummary }));
}
