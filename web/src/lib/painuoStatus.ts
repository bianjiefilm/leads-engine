import type { StatusState } from "@/vendor/painuo/react/v1/src/index";

// HUI-2622 R 面：roiStatus → painuo renderer StatusState 纯映射。
// 无 IO、无 React、无分支副作用；未知值一律降级到中性 queued，绝不映射成成功色。
export const DEGRADED_STATE: StatusState = "queued";

const ROI_STATUS_TO_STATE: Record<string, StatusState> = {
  complete: "resultReady",
  incomplete: "needsAttention",
  source_stats_only: "waiting",
};

export function roiStatusToState(roiStatus: string): StatusState {
  const known =
    typeof roiStatus === "string" && Object.hasOwn(ROI_STATUS_TO_STATE, roiStatus)
      ? ROI_STATUS_TO_STATE[roiStatus]
      : null;
  return known ?? DEGRADED_STATE;
}
