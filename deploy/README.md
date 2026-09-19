# deploy 配置样例

两个样例文件只含键名与说明,**永不写入真实 token**;真值按 public-ai checklist v3.1 惯例放服务器
`/etc/leads-engine/*.env`(0640,不进 git)。

- `leads-server.env.example`:Go 服务。含 app 配置、platform identity(必配)、notify/upload(默认 off)。
- `leads-web.env.example`:Next.js BFF。只指向 loopback 的 Go 服务。

键名约定:

- 平台服务键名对齐 `PLATFORM_*_BASE_URL` / `PLATFORM_*_TOKEN`(checklist v3.1 §3.2);
  其中 identity 专用令牌由平台侧写入 `IDENTITY_APP_TOKENS`(每 app × 每服务一把,禁止万能 token)。
- 本应用自有键以 `LEADS_` 前缀;`FEATURE_NOTIFY`/`FEATURE_UPLOAD` 默认 off。
