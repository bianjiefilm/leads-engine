import { describe, expect, it } from "vitest";
import { channelsDeliverable, leadPurposeAllowed, newVisitorKey } from "../src/lib/reception";

describe("reception presentation gate", () => {
  it("allows text and audio only for the current approved reply", () => {
    expect(channelsDeliverable(2, 2, "approved")).toEqual({ text: true, audio: true });
    expect(channelsDeliverable(1, 2, "approved")).toEqual({ text: false, audio: false });
    expect(channelsDeliverable(2, 2, "sent")).toEqual({ text: false, audio: false });
    expect(channelsDeliverable(2, 2, "superseded")).toEqual({ text: false, audio: false });
  });

  it("does not turn after-sales into a lead", () => {
    expect(leadPurposeAllowed("sales_followup", true)).toBe(true);
    expect(leadPurposeAllowed("after_sales", true)).toBe(false);
    expect(leadPurposeAllowed("general_qa", true)).toBe(false);
    expect(leadPurposeAllowed("sales_followup", false)).toBe(false);
  });

  it("mints a visitor key that is not a principal id", () => {
    const key = newVisitorKey();
    expect(key.startsWith("usr_")).toBe(false);
    expect(key.length).toBeGreaterThanOrEqual(16);
  });
});
