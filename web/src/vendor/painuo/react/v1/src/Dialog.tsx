"use client";
import React, { forwardRef } from "react";
import { Dialog as BaseDialog } from "@base-ui/react/dialog";
import { X } from "lucide-react";
import { ScopedPortal } from "./internal/ScopedPortal";
import { ModalPresence } from "./internal/ModalPresence";
import { IconButton } from "./IconButton";
export interface DialogProps {
 open:boolean;onOpenChange:(open:boolean)=>void;title:string;description?:string;children:React.ReactNode;footer?:React.ReactNode;
 busy?:boolean;size?:"sm"|"md"|"lg";dismissOnOutsidePress?:boolean;
 initialFocus?:React.RefObject<HTMLElement|null>;finalFocus?:React.RefObject<HTMLElement|null>;
}
export const Dialog=forwardRef<HTMLDivElement,DialogProps>(function Dialog({open,onOpenChange,title,description,children,footer,busy=false,size="md",dismissOnOutsidePress=false,initialFocus,finalFocus},ref){
 if(typeof open!=="boolean"||typeof onOpenChange!=="function"||typeof title!=="string"||!title.trim()||!["sm","md","lg"].includes(size))throw new Error("renderer dialog props invalid");
 return <BaseDialog.Root open={open} onOpenChange={(next,details)=>{if(busy){details.cancel();return;}onOpenChange(next);}} disablePointerDismissal={!dismissOnOutsidePress}>
 <ModalPresence open={open}/><ScopedPortal kind="dialog"><BaseDialog.Backdrop className="pn-r-backdrop"/><BaseDialog.Popup ref={ref} initialFocus={initialFocus} finalFocus={finalFocus} className={`pn-r-popup pn-r-popup-${size}`} aria-busy={busy||undefined}>
 <div className="pn-r-dialog-heading"><BaseDialog.Title className="pn-r-dialog-title">{title}</BaseDialog.Title><IconButton label="关闭" icon={X} variant="ghost" disabled={busy} onClick={()=>onOpenChange(false)}/></div>
 {description&&<BaseDialog.Description className="pn-r-description">{description}</BaseDialog.Description>}<div className="pn-r-dialog-content">{children}</div>{footer&&<div className="pn-r-dialog-footer">{footer}</div>}
 </BaseDialog.Popup></ScopedPortal></BaseDialog.Root>;
});
