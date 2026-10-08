"use client";

import { useCallback, useEffect, useState } from "react";
import { scopeInit, useCrmScope } from "@/lib/eco-nav/use-crm-scope";
import { failureText } from "@/lib/productShell";
import { draftStatus, presentLightCopy, type LightCapability } from "@/lib/lightCopy";

interface DraftRow {
  id: string;
  kind: string;
  body: string;
  content_version: number;
  user_confirmed?: boolean;
}

const KINDS = [
  { id: "reply", label: "回复" },
  { id: "email", label: "邮件" },
  { id: "marketing_brief", label: "营销简介" },
] as const;

const TOOLS = [
  { id: "goboost", label: "GoBoost" },
  { id: "product_image", label: "产品图" },
  { id: "digital_human", label: "数字人" },
  { id: "aicut", label: "AiCut" },
] as const;

export function LightCopyPanel({
  subjectKind,
  subjectId,
}: {
  subjectKind: "opportunity" | "campaign";
  subjectId: string;
}) {
  const scope = useCrmScope();
  const [kind, setKind] = useState<(typeof KINDS)[number]["id"]>("reply");
  const [body, setBody] = useState("");
  const [drafts, setDrafts] = useState<DraftRow[]>([]);
  const [note, setNote] = useState(presentLightCopy(null).note);
  const [statusLabel, setStatusLabel] = useState("草稿，未发送");
  const [message, setMessage] = useState("");
  const [facts, setFacts] = useState("");
  const [selected, setSelected] = useState<string[]>(subjectKind === "campaign" ? ["activity_ref"] : ["title"]);
  const [tool, setTool] = useState<(typeof TOOLS)[number]["id"]>("goboost");
  const [decline, setDecline] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hidden, setHidden] = useState(false);

  const base = subjectKind === "campaign" ? `/api/campaigns/${subjectId}/light-copy` : `/api/opportunities/${subjectId}/light-copy`;

  const applyDraft = useCallback((row: DraftRow | undefined) => {
    if (!row) {
      setBody("");
      setStatusLabel(draftStatus(null).label);
      return;
    }
    setBody(row.body);
    setStatusLabel(draftStatus(row).label);
  }, []);

  const load = useCallback(async () => {
    if (!subjectId || !scope.tenant) return;
    const init = scopeInit(scope.tenant);
    const capRes = await fetch("/api/light-copy/capability", init);
    if (capRes.status === 404) {
      setHidden(true);
      return;
    }
    const cap = (await capRes.json()) as LightCapability;
    setNote(presentLightCopy(cap).note);
    const listRes = await fetch(base, init);
    if (listRes.status === 404) {
      setMessage(subjectKind === "campaign" ? "当前线索不是活动，轻文案只使用商机或碰一碰活动。" : "找不到这条商机。");
      setDrafts([]);
      return;
    }
    if (!listRes.ok) {
      const errBody = await listRes.json();
      setMessage(errBody.message ?? "加载失败");
      return;
    }
    const list = await listRes.json();
    const items = (list.items ?? []) as DraftRow[];
    setDrafts(items);
    applyDraft(items.find((item) => item.kind === kind));
  }, [applyDraft, base, kind, scope.tenant, subjectId, subjectKind]);

  useEffect(() => {
    void load();
  }, [load]);

  function toggleFact(key: string) {
    setSelected((cur) => (cur.includes(key) ? cur.filter((item) => item !== key) : [...cur, key]));
  }

  async function call(path: string, method: string, payload: Record<string, unknown>) {
    setBusy(true);
    setMessage("");
    try {
      const res = await fetch(path, scopeInit(scope.tenant, {
        method,
        headers: { "content-type": "application/json" },
        body: JSON.stringify(payload),
      }));
      const data = await res.json();
      if (!res.ok) {
        setMessage(failureText(data, res.status));
        return data;
      }
      return data;
    } catch (err) {
      setMessage(err instanceof Error ? err.message : "请求失败");
      return null;
    } finally {
      setBusy(false);
    }
  }

  if (hidden) return null;

  const factChoices = subjectKind === "campaign" ? ["activity_ref"] : ["title", "price_cents"];

  return (
    <section className="card">
      <h2>轻文案</h2>
      <p className="muted">{note}</p>
      <p>{statusLabel}</p>
      <p>
        {KINDS.map((item) => (
          <button
            key={item.id}
            type="button"
            disabled={busy}
            onClick={() => {
              setKind(item.id);
              applyDraft(drafts.find((row) => row.kind === item.id));
            }}
          >
            {item.label}
          </button>
        ))}
      </p>
      <label>
        草稿
        <textarea value={body} rows={5} cols={60} onChange={(e) => setBody(e.target.value)} />
      </label>
      <p>
        <button
          type="button"
          disabled={busy || !body.trim()}
          onClick={async () => {
            const saved = await call(`${base}/${kind}`, "PUT", { body });
            if (saved?.body) {
              setStatusLabel(draftStatus(saved).label);
              setMessage("已保存。尚未发送。");
              void load();
            }
          }}
        >
          保存草稿
        </button>{" "}
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            await call(`${base}/${kind}/generate`, "POST", { selected });
          }}
        >
          生成
        </button>{" "}
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            const confirmed = await call(`${base}/${kind}/confirm`, "POST", {});
            if (confirmed?.user_confirmed) setStatusLabel(draftStatus(confirmed).label);
          }}
        >
          确认草稿（不会发送）
        </button>
      </p>
      <p>
        交给后续工具的事实：
        {factChoices.map((key) => (
          <label key={key}>
            <input type="checkbox" checked={selected.includes(key)} onChange={() => toggleFact(key)} /> {key}
          </label>
        ))}{" "}
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            const data = await call(`${base}/${kind}/facts`, "POST", { selected });
            if (data?.facts) setFacts(JSON.stringify(data.facts));
            else setFacts("");
          }}
        >
          预览事实
        </button>
      </p>
      {facts ? <p className="muted">事实：{facts}</p> : null}
      <p>
        <label>
          专业工具
          <select value={tool} onChange={(e) => setTool(e.target.value as (typeof TOOLS)[number]["id"])}>
            {TOOLS.map((item) => (
              <option key={item.id} value={item.id}>
                {item.label}
              </option>
            ))}
          </select>
        </label>{" "}
        <label>
          <input type="checkbox" checked={decline} onChange={(e) => setDecline(e.target.checked)} /> 不使用专业工具
        </label>{" "}
        <button
          type="button"
          disabled={busy}
          onClick={async () => {
            const data = await call(`${base}/${kind}/handoff`, "POST", {
              tool,
              idempotency_key: `${subjectId}:${kind}`,
              decline,
              selected,
            });
            if (data?.status) {
              setMessage(
                data.status === "declined"
                  ? "已拒绝专业工具。草稿仍可保存和手改。"
                  : `${data.reason ?? data.status}。没有新建制作工程，也没有重新生成。`,
              );
            }
          }}
        >
          交接
        </button>
      </p>
      {message ? <p className="muted">{message}</p> : null}
    </section>
  );
}
