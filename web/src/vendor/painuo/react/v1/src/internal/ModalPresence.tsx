"use client";
import { useEffect } from "react";
import { useScopeValue } from "./ScopeContext";
/** Notification deferral only. Base UI remains responsible for modal interaction. */
export function ModalPresence({open}:{open:boolean}) {
 const {registerModal,portalRoot}=useScopeValue();
 useEffect(()=>{if(open&&portalRoot?.isConnected)return registerModal();},[open,portalRoot,registerModal]);
 return null;
}
