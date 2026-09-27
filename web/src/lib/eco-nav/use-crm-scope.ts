"use client";

import { useSyncExternalStore } from "react";
import { CRM_TENANT_STORAGE_KEY, emptyCrmCache, switchCrmTenant, type CrmCache } from "@/lib/eco-nav/crm-scope";

let cache: CrmCache = emptyCrmCache();
const listeners = new Set<() => void>();
const serverCache = emptyCrmCache();

function emit() {
  listeners.forEach((listener) => listener());
}

export function commitCrmTenant(next: string) {
  const updated = switchCrmTenant(cache, next);
  if (updated === cache) return;
  cache = updated;
  if (typeof window !== "undefined") {
    window.localStorage.setItem(CRM_TENANT_STORAGE_KEY, updated.tenantId ?? "");
  }
  emit();
}

export function hydrateCrmTenantFromStorage() {
  if (typeof window === "undefined") return;
  const saved = window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "";
  if (saved.trim()) commitCrmTenant(saved);
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useCrmScope(): CrmCache {
  return useSyncExternalStore(subscribe, () => cache, () => serverCache);
}

export function scopeInit(tenantId: string | null, init: RequestInit = {}): RequestInit {
  const headers = new Headers(init.headers);
  if (tenantId) headers.set("x-tenant-id", tenantId);
  return { ...init, headers };
}
