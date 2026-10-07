"use client";
import React, { forwardRef, useEffect, useId, useRef, useState } from "react";
import { Button as BaseButton } from "@base-ui/react/button";
import { LoaderCircle } from "lucide-react";
import { createPendingGate } from "./internal/PendingGate";
import { SafeError } from "./internal/SafeError";
import { useRendererScope } from "./internal/ScopeContext";
type NativeProps = Omit<React.ButtonHTMLAttributes<HTMLButtonElement>,"onClick"|"disabled"|"children"|"type"|"style"|"dangerouslySetInnerHTML">;
type Callback = {onClick?:React.MouseEventHandler<HTMLButtonElement>;onAction?:never}|{onClick?:never;onAction:()=>void|Promise<void>};
export type ButtonProps = NativeProps & Callback & {
 children?:React.ReactNode;type?:"button"|"submit"|"reset";disabled?:boolean;loading?:boolean;disabledReason?:string;
 variant?:"primary"|"secondary"|"ghost"|"danger";size?:"sm"|"md"|"lg";safeMessage?:string;
};
export const Button=forwardRef<HTMLButtonElement,ButtonProps>(function Button({children,type="button",disabled=false,loading=false,disabledReason,variant="primary",size="md",safeMessage,onClick,onAction,className,"aria-describedby":describedBy,...native},ref){
 useRendererScope();const reasonId=useId(),errorId=useId();const gate=useRef(createPendingGate());const [pending,setPending]=useState(false),[error,setError]=useState(false);const mounted=useRef(true);
 useEffect(()=>{mounted.current=true;return ()=>{mounted.current=false;};},[]);
 if(onClick&&onAction)throw new Error("renderer button callbacks invalid");
 if(!["button","submit","reset"].includes(type)||!["primary","secondary","ghost","danger"].includes(variant)||!["sm","md","lg"].includes(size))throw new Error("renderer button props invalid");
 for(const key of ["render","nativeButton","style","dangerouslySetInnerHTML"])if(Object.hasOwn(native,key))throw new Error("renderer button props invalid");
 const blocked=disabled||loading||pending;
 async function activate(){
  if(disabled||loading||!onAction||!gate.current.tryEnter())return;
  setPending(true);setError(false);
  try{await onAction();}catch{if(mounted.current)setError(true);}finally{gate.current.leave();if(mounted.current)setPending(false);}
 }
 const descriptions=[describedBy,disabledReason?reasonId:null,error?errorId:null].filter(Boolean).join(" ")||undefined;
 return <span className="pn-r-control"><BaseButton {...native} ref={ref} type={type} disabled={blocked} aria-busy={loading||pending||undefined} aria-describedby={descriptions} className={`pn-r-button pn-r-${variant} pn-r-size-${size}${className?` ${className}`:""}`} onClick={event=>{if(disabled||loading||gate.current.pending)return;if(onAction){void activate();}else{onClick?.(event);}}}>
 {children}{(loading||pending)&&<LoaderCircle className="pn-r-spinner" aria-hidden="true" focusable="false"/>}
 </BaseButton>{disabledReason&&<span className="pn-r-reason" id={reasonId}>{disabledReason}</span>}{error&&<SafeError id={errorId} message={safeMessage}/>}</span>;
});
