"use client";
import React, { forwardRef, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { AlertDialog } from "@base-ui/react/alert-dialog";
import { ScopedPortal } from "./internal/ScopedPortal";
import { ModalPresence } from "./internal/ModalPresence";
import { createPendingGate } from "./internal/PendingGate";
import { SafeError } from "./internal/SafeError";
import { Button } from "./Button";
export interface ConfirmDialogProps {
 open:boolean;onOpenChange:(open:boolean)=>void;title:string;consequence:string;confirmLabel:string;cancelLabel:string;
 onConfirm:()=>void|Promise<void>;danger?:boolean;confirmPhrase?:string;safeMessage?:string;finalFocus?:React.RefObject<HTMLElement|null>;
}
// Opening ownership changes only after a DOM commit; SSR has no layout effect.
const useCommitEffect=typeof window==="undefined"?useEffect:useLayoutEffect;
export const ConfirmDialog=forwardRef<HTMLDivElement,ConfirmDialogProps>(function ConfirmDialog({open,onOpenChange,title,consequence,confirmLabel,cancelLabel,onConfirm,danger=false,confirmPhrase,safeMessage,finalFocus},ref){
 const phraseId=useId(),helpId=useId();const gate=useRef(createPendingGate());const cancelRef=useRef<HTMLButtonElement>(null);const mounted=useRef(true);const opening=useRef<object>({});
 const [pending,setPending]=useState(false),[error,setError]=useState(false),[phrase,setPhrase]=useState("");
 useCommitEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;opening.current={};};},[]);
 useCommitEffect(()=>{opening.current={};if(!open){setPhrase("");setError(false);}},[open]);
 if(typeof open!=="boolean"||typeof onOpenChange!=="function"||typeof onConfirm!=="function"||[title,consequence,confirmLabel,cancelLabel].some(v=>typeof v!=="string"||!v.trim())||(confirmPhrase!==undefined&&typeof confirmPhrase!=="string"))throw new Error("renderer confirm props invalid");
 const phraseMatches=confirmPhrase===undefined||phrase===confirmPhrase;
 async function confirm(){
  if(!open||!phraseMatches||!gate.current.tryEnter())return;
  const owner=opening.current;setPending(true);setError(false);
  try{await onConfirm();}catch{if(mounted.current&&opening.current===owner)setError(true);}finally{gate.current.leave();if(mounted.current)setPending(false);}
 }
 const requestClose=()=>{if(!gate.current.pending)onOpenChange(false);};
 return <AlertDialog.Root open={open} onOpenChange={(next,details)=>{if(gate.current.pending){details.cancel();return;}onOpenChange(next);}}>
 <ModalPresence open={open}/><ScopedPortal kind="confirm"><AlertDialog.Backdrop className="pn-r-backdrop"/><AlertDialog.Popup ref={ref} initialFocus={cancelRef} finalFocus={finalFocus} className="pn-r-popup pn-r-popup-md" aria-busy={pending||undefined}>
 <AlertDialog.Title className="pn-r-dialog-title">{title}</AlertDialog.Title><AlertDialog.Description className="pn-r-description">{consequence}</AlertDialog.Description>
 {confirmPhrase!==undefined&&<div className="pn-r-phrase"><label id={phraseId} htmlFor={`${phraseId}-input`}>确认文本</label><input id={`${phraseId}-input`} aria-labelledby={phraseId} aria-describedby={helpId} className="pn-r-input" value={phrase} disabled={pending} onChange={event=>setPhrase(event.target.value)}/><span id={helpId} className="pn-r-reason">请输入：{confirmPhrase}</span></div>}
 {error&&<SafeError message={safeMessage}/>}<div className="pn-r-dialog-footer"><Button ref={cancelRef} variant="secondary" disabled={pending} onClick={requestClose}>{cancelLabel}</Button><Button variant={danger?"danger":"primary"} disabled={!phraseMatches||pending} loading={pending} onClick={()=>{void confirm();}}>{confirmLabel}</Button></div>
 </AlertDialog.Popup></ScopedPortal></AlertDialog.Root>;
});
