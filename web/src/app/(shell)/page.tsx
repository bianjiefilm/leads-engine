"use client";

import { useEffect, useState } from "react";

// L0 页面骨架:只打本站 /api/*(BFF),绝不直连 Go 服务或平台服务。
// 客户档案、商机与线索列表打本站 BFF。销售工作台(HUI-1893)不在本页。

interface Whoami {
  principal_ref?: string;
  email?: string;
  tenant_id?: string;
  role?: string;
  enabled?: boolean;
  agent_grant?: boolean;
  error?: string;
}

export default function Home() {
  const [me, setMe] = useState<Whoami | null>(null);
  const [err, setErr] = useState<string>("");

  useEffect(() => {
    fetch("/api/whoami")
      .then(async (res) => {
        const body = await res.json();
        if (!res.ok) {
          setErr(body.message ?? body.error ?? `HTTP ${res.status}`);
        } else {
          setMe(body);
        }
      })
      .catch((e: Error) => setErr(e.message));
  }, []);

  return (
    <main>
      <h1>数海获客 · 独立商家 CRM</h1>
      <div className="card">
        <h2>会话状态</h2>
        {err ? (
          <p className="muted">未登录或会话不可用:{err}</p>
        ) : me ? (
          <ul>
            <li>
              成员:<code>{me.principal_ref}</code>({me.email})
            </li>
            <li>
              租户:<code>{me.tenant_id}</code> · 角色:<code>{me.role}</code>
            </li>
            <li>
              agent 逐租户授权:{me.agent_grant ? "有" : "无"}
            </li>
          </ul>
        ) : (
          <p className="muted">加载中…</p>
        )}
      </div>
      <div className="card">
        <h2>L0 底座范围</h2>
        <p className="muted">
          本应用是独立商家 CRM:不内嵌接单数据库、不做平台全局客户库。
          平台登录经 platform-identity。Notify 授权线索进入
          <a href="/leads">线索列表（来源与待分配原因可见）</a>。
          企业资料筛选(HUI-1678)只接受客户有权导入的资料，官方公开库未接入：
          <a href="/enterprises">进入企业资料筛选</a>。
          客户档案(HUI-1691)已就绪:<a href="/contacts">进入客户档案(授权按来源保留、撤销不可恢复)</a>。
          商机管理(HUI-1693)已就绪:
          <a href="/opportunities">进入商机管理(按 商家经营销售 / 创意服务 类别隔离)</a>。
          统一接待(HUI-1688):<a href="/reception">接待工作台</a>。
          意向分级(HUI-1684):<a href="/intent">查看规则评分与人工修正</a>。只给下一步建议，不自动触达。
        </p>
      </div>
    </main>
  );
}
