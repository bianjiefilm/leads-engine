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
    window.localStorage.setItem(CRM_TENANT_STORAGE_KEY, updated.tenant ?? "");
  }
  emit();
}

export function hydrateCrmTenantFromStorage() {
  if (typeof window === "undefined") return;
  const saved = window.localStorage.getItem(CRM_TENANT_STORAGE_KEY) ?? "";
  if (saved.trim()) commitCrmTenant(saved);
}

// 对账结果为空时清掉内存里的范围，不改已经保存的登录租户。
export function releaseCrmTenant() {
  if (!cache.tenant) return;
  cache = {
    tenant: null,
    epoch: cache.epoch + 1,
    contacts: null,
    opportunities: null,
    leads: null,
    workbench: null,
    filters: {},
    details: {},
  };
  emit();
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function useCrmScope(): CrmCache {
  return useSyncExternalStore(subscribe, () => cache, () => serverCache);
}

export function scopeInit(tenant: string | null, init: RequestInit = {}): RequestInit {
  const headers = new Headers(init.headers);
  if (tenant) headers.set("x-tenant-id", tenant);
  return { ...init, headers };
}
