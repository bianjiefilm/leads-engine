// SOP 和外呼只使用已经选中的客户、授权和活动。
// 缺客户、缺营销授权或缺活动时不提交，也不补一个空 id。

export interface ScopeContact {
  id: string;
  name: string;
  campaign_id?: string;
}

export interface ScopeConsent {
  id: string;
  purpose: string;
  source_channel: string;
  marketing_allowed: true;
  revoked_at: null;
}

export interface SopPayload {
  contact_id: string;
  channel: string;
  recipient: string;
  purpose: "follow_up" | "marketing";
  content_version: 1;
  budget_cents: 0;
  body: string;
  consent_id?: string;
}

export interface OutboundPayload {
  task_key: string;
  contact_id: string;
  campaign_id: string;
  mode: "isolation";
  budget_cents: 0;
  op: "dial";
  consent_id?: string;
}

export interface SopSubmitInput {
  contact?: unknown;
  purpose?: unknown;
  consent?: unknown;
  channel?: unknown;
  recipient?: unknown;
  body?: unknown;
}

export interface OutboundSubmitInput {
  contact?: unknown;
  consent?: unknown;
  newKey?: () => string;
}

const CHANNELS = new Set(["sms", "email", "wecom"]);

function text(value: unknown): string | null {
  if (typeof value !== "string") return null;
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

function row(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== "object") return null;
  return value as Record<string, unknown>;
}

function revokedEmpty(value: unknown): boolean {
  if (value == null) return true;
  if (typeof value === "string") return value.trim() === "";
  return false;
}

export function contactChoices(items: unknown): ScopeContact[] {
  if (!Array.isArray(items)) return [];
  const out: ScopeContact[] = [];
  for (const item of items) {
    const record = row(item);
    if (!record) continue;
    const id = text(record.id);
    const name = text(record.name);
    if (!id || !name) continue;
    const campaign = text(record.campaign_id);
    out.push(campaign ? { id, name, campaign_id: campaign } : { id, name });
  }
  return out;
}

export function consentChoices(items: unknown): ScopeConsent[] {
  if (!Array.isArray(items)) return [];
  const out: ScopeConsent[] = [];
  for (const item of items) {
    const record = row(item);
    if (!record) continue;
    const id = text(record.id);
    if (!id || record.marketing_allowed !== true || !revokedEmpty(record.revoked_at)) continue;
    out.push({
      id,
      purpose: text(record.purpose) ?? "",
      source_channel: text(record.source_channel) ?? "",
      marketing_allowed: true,
      revoked_at: null,
    });
  }
  return out;
}

export function campaignChoice(contact: unknown): string | null {
  const record = row(contact);
  if (!record) return null;
  return text(record.campaign_id);
}

function oneContact(contact: unknown): ScopeContact | null {
  return contactChoices(contact == null ? [] : [contact])[0] ?? null;
}

function oneConsent(consent: unknown): ScopeConsent | null {
  return consentChoices(consent == null ? [] : [consent])[0] ?? null;
}

export function sopSubmit(input: SopSubmitInput): SopPayload | null {
  const contact = oneContact(input.contact);
  if (!contact) return null;
  if (input.purpose !== "follow_up" && input.purpose !== "marketing") return null;
  const channel = text(input.channel);
  const recipient = text(input.recipient);
  const body = text(input.body);
  if (!channel || !CHANNELS.has(channel) || !recipient || !body) return null;
  const payload: SopPayload = {
    contact_id: contact.id,
    channel,
    recipient,
    purpose: input.purpose,
    content_version: 1,
    budget_cents: 0,
    body,
  };
  if (input.purpose === "follow_up") return payload;
  const consent = oneConsent(input.consent);
  if (!consent) return null;
  payload.consent_id = consent.id;
  return payload;
}

export function outboundSubmit(input: OutboundSubmitInput): OutboundPayload | null {
  const contact = oneContact(input.contact);
  if (!contact) return null;
  const campaign = campaignChoice(contact);
  if (!campaign) return null;
  const taskKey = text(input.newKey?.() ?? "");
  if (!taskKey) return null;
  const payload: OutboundPayload = {
    task_key: taskKey,
    contact_id: contact.id,
    campaign_id: campaign,
    mode: "isolation",
    budget_cents: 0,
    op: "dial",
  };
  const consent = oneConsent(input.consent);
  if (consent) payload.consent_id = consent.id;
  return payload;
}
