"use client";
import React, { createContext, useContext, useState } from "react";
import { Dialog } from "@base-ui/react/dialog";
import { AlertDialog } from "@base-ui/react/alert-dialog";
import { Select } from "@base-ui/react/select";
import { Tooltip } from "@base-ui/react/tooltip";
import { Popover } from "@base-ui/react/popover";
import { Menu } from "@base-ui/react/menu";
import { Drawer } from "@base-ui/react/drawer";
import { Toast } from "@base-ui/react/toast";
import { useScopeValue } from "./ScopeContext";
interface OwnedParent { ownerRoot: HTMLElement; portal: HTMLDivElement | null }
const NestedPortalContext=createContext<OwnedParent|null>(null);
function useOwnedPortalTarget() {
  const scope = useScopeValue(),
    inherited = useContext(NestedPortalContext);
  const owner = scope.portalRoot;
  // A different provider identity resets private nesting exactly as ScopedPortal does.
  const parent = inherited?.ownerRoot === owner ? inherited : null;
  return { scope, owner, target: parent ? parent.portal : owner };
}
// Internal callback admission only; not a renderer namespace handle or public API.
export function useScopedPortalTargetConnected() {
  const { owner, target } = useOwnedPortalTarget();
  return () =>
    Boolean(
      owner?.isConnected &&
      target?.isConnected &&
      owner.contains(target) &&
      target.closest('.pn-r-portal') === owner,
    );
}
export function ScopedPortal({kind,children}:{kind:"dialog"|"confirm"|"select"|"tooltip"|"popover"|"menu"|"drawer"|"toast";children:React.ReactNode}) {
 const {scope,owner,target}=useOwnedPortalTarget();const [localPortal,setLocalPortal]=useState<HTMLDivElement|null>(null);
 if(!owner?.isConnected||!target?.isConnected||!owner.contains(target)||target.closest(".pn-r-portal")!==owner)return null;
 // Base UI marks outside DOM inert. Its child portal must be physically inside its parent portal.
 // Wait for a real ref rather than falling back to body or an unrelated ancestor.
 const content=<NestedPortalContext.Provider value={{ownerRoot:owner,portal:localPortal}}>{children}</NestedPortalContext.Provider>;
 const attrs={"data-pn-surface":scope.surface,"data-pn-theme":scope.theme,"data-pn-profile":scope.profile,"data-pn-density":scope.density};

 if(kind==="tooltip")return <Tooltip.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Tooltip.Portal>;
 if(kind==="popover")return <Popover.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Popover.Portal>;
 if(kind==="menu")return <Menu.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Menu.Portal>;
 if(kind==="drawer")return <Drawer.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Drawer.Portal>;
 if(kind==="toast")return <Toast.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Toast.Portal>;
 return kind==="select"?<Select.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Select.Portal>:kind==="dialog"?<Dialog.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</Dialog.Portal>:<AlertDialog.Portal {...attrs} ref={setLocalPortal} container={target}>{content}</AlertDialog.Portal>;
}
