// 订阅展示。三本账分开，钱包余额不打开套餐，商家成交额不进平台钱包。

export interface SubscriptionFacts {
  status?: string;
  wallet_balance_cents?: number | null;
  crm_subscription_cents?: number | null;
  ai_usage_cents?: number | null;
  merchant_deal_cents?: number | null;
  history_retained?: boolean;
  data_lost?: boolean;
}

export interface LedgerLine {
  key: "crm_subscription" | "ai_usage" | "merchant_deal";
  label: string;
  cents: number | null;
}

export function presentSubscription(input: SubscriptionFacts): {
  crmOpened: boolean;
  openedByWallet: false;
  lines: LedgerLine[];
  walletCents: number | null;
  merchantRevenueInWallet: false;
  historyRetained: true;
  dataLost: false;
  advancedLimited: boolean;
} {
  const wallet = input.wallet_balance_cents ?? null;
  return {
    crmOpened: input.status === "active",
    openedByWallet: false,
    lines: [
      { key: "crm_subscription", label: "CRM 订阅", cents: input.crm_subscription_cents ?? null },
      { key: "ai_usage", label: "AI 增值费用", cents: input.ai_usage_cents ?? null },
      { key: "merchant_deal", label: "商家成交额", cents: input.merchant_deal_cents ?? null },
    ],
    walletCents: wallet,
    merchantRevenueInWallet: false,
    historyRetained: true,
    dataLost: false,
    advancedLimited: input.status === "expired",
  };
}

export function rechargeReturn(input: {
  reason?: string;
  billing_center?: string;
  return_to?: string;
  live_charge?: number;
}): { entry: string; returnTo: string; charges: false; rereadFirst: boolean } {
  return {
    entry: input.billing_center === "billing_center" ? "billing_center" : "",
    returnTo: input.return_to ?? "",
    charges: false,
    rereadFirst: input.reason === "insufficient_balance" || input.reason === "quote_stale",
  };
}

export function centsLabel(cents: number | null): string {
  if (cents == null) return "未知";
  return "¥" + (cents / 100).toFixed(2);
}
