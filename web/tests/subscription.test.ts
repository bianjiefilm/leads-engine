import { describe, expect, it } from "vitest";
import { presentSubscription, rechargeReturn } from "@/lib/subscription";

describe("subscription ledgers", () => {
  it("keeps CRM subscription, AI usage, and merchant revenue apart from the wallet", () => {
    const view = presentSubscription({
      status: "expired",
      wallet_balance_cents: 5_000_000,
      crm_subscription_cents: 1_200,
      ai_usage_cents: 300,
      merchant_deal_cents: 8_000_000,
    });
    expect(view.crmOpened).toBe(false);
    expect(view.openedByWallet).toBe(false);
    expect(view.lines.map((line) => [line.key, line.cents])).toEqual([
      ["crm_subscription", 1_200],
      ["ai_usage", 300],
      ["merchant_deal", 8_000_000],
    ]);
    expect(view.walletCents).toBe(5_000_000);
    expect(view.walletCents).not.toBe(view.lines[2].cents);
    expect(view.merchantRevenueInWallet).toBe(false);
    expect(view.historyRetained).toBe(true);
    expect(view.dataLost).toBe(false);
  });

  it("sends an underfunded AI action back to the same place after recharge", () => {
    const back = rechargeReturn({
      reason: "insufficient_balance",
      billing_center: "billing_center",
      return_to: "/leads/lead_1",
      live_charge: 0,
    });
    expect(back.entry).toBe("billing_center");
    expect(back.returnTo).toBe("/leads/lead_1");
    expect(back.charges).toBe(false);
    expect(back.rereadFirst).toBe(true);
  });
});
