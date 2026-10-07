"use client";
import React, { forwardRef } from "react";
import type { LucideIcon } from "lucide-react";
import { Button, type ButtonProps } from "./Button";
type IconProps<T> = T extends unknown ? Omit<T,"children"|"aria-label"> & {label:string;icon:LucideIcon} : never;
export type IconButtonProps = IconProps<ButtonProps>;
export const IconButton=forwardRef<HTMLButtonElement,IconButtonProps>(function IconButton({label,icon:Icon,className,...props},ref){
 if(typeof label!=="string"||!label.trim())throw new Error("renderer label required");
 return <Button {...props} ref={ref} aria-label={label} className={`pn-r-icon-button${className?` ${className}`:""}`}><Icon aria-hidden="true" focusable="false"/></Button>;
});
