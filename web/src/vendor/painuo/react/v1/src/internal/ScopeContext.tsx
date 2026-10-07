"use client";
import React, { createContext, useContext } from "react";
export interface RendererScope {
 readonly profile: string; readonly surface: string; readonly theme: "light" | "dark";
 readonly density: "compact" | "default" | "touch";
 readonly portalRoot: HTMLElement | null; readonly modalActive: boolean;
}
export interface ScopeValue extends RendererScope { registerModal(): () => void }
export const ScopeContext = createContext<ScopeValue | null>(null);
export function useScopeValue(): ScopeValue {
 const scope = useContext(ScopeContext);
 if (!scope) throw new Error("renderer provider required");
 return scope;
}
export function useRendererScope(): RendererScope {
 const {profile,surface,theme,density,portalRoot,modalActive}=useScopeValue();
 return {profile,surface,theme,density,portalRoot,modalActive};
}
