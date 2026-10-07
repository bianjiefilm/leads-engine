"use client";
import React, { useCallback, useId, useMemo, useState } from "react";
import scopes from "../contract/profile-scopes.json";
import { ScopeContext, type RendererScope } from "./internal/ScopeContext";
export { useRendererScope } from "./internal/ScopeContext";
export interface RendererProviderProps {
 profile: string; surface: string; theme: "light" | "dark"; children: React.ReactNode;
 density?: RendererScope["density"]; externalModalActive?: boolean;
}
export function RendererProvider({profile,surface,theme,children,density="default",externalModalActive=false}:RendererProviderProps) {
 const id=useId();const [portalRoot,setPortalRoot]=useState<HTMLElement|null>(null);const [modalCount,setModalCount]=useState(0);
 const registerModal=useCallback(()=>{let active=true;setModalCount(n=>n+1);return ()=>{if(active){active=false;setModalCount(n=>n-1);}};},[]);
 const profileTable: Record<string,{reactEligible:boolean;surfaceThemes:string[]}>=scopes.profiles;
 if(typeof profile!=="string"||typeof surface!=="string"||typeof theme!=="string")throw new Error("renderer scope invalid");
 const owned=Object.hasOwn(profileTable,profile)?profileTable[profile]:null;
 if(!owned?.reactEligible || !owned.surfaceThemes.includes(`${surface}/${theme}`) || !["compact","default","touch"].includes(density) || typeof externalModalActive!=="boolean")throw new Error("renderer scope invalid");
 const value=useMemo(()=>({profile,surface,theme,density,portalRoot,modalActive:modalCount>0||externalModalActive,registerModal}),[profile,surface,theme,density,portalRoot,modalCount,externalModalActive,registerModal]);
 return <ScopeContext.Provider value={value}><div className="pn-r-scope" data-pn-profile={profile} data-pn-surface={surface} data-pn-theme={theme} data-pn-density={density} data-pn-owner={id}>
 {children}<div className="pn-r-portal" data-pn-profile={profile} data-pn-surface={surface} data-pn-theme={theme} data-pn-density={density} data-pn-owner={id} ref={setPortalRoot}/>
 </div></ScopeContext.Provider>;
}
