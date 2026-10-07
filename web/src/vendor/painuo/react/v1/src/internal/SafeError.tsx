"use client";
import React from "react";
/** Deliberately receives no Error or response object. */
export function SafeError({id,message="操作未完成，请检查后重试。"}:{id?:string;message?:string}) {
 return <p id={id} className="pn-r-error" role="alert">{message}</p>;
}
