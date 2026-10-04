// 从页面普查里只抽出 6 个代表页。只记账，不改页面。
// 按传入行的现有顺序取第一条，不按字母重排。截图仍是 not_measured。

import { NOT_MEASURED, pageCensus } from "./pageCensus";

export interface RepresentativeInput {
  path: string;
  page_file: string;
  id: string;
  surface: string;
  stack: string;
}

export interface PageRepresentative {
  role: "Entry" | "Home" | "List" | "Detail" | "Main Action" | "Result";
  route: string;
  file: string;
  id: string;
  surface: string;
  stack: string;
  screenshot: typeof NOT_MEASURED;
}

const NOT_PRESENT = "not_present";

function record(role: PageRepresentative["role"], row: RepresentativeInput | undefined): PageRepresentative {
  if (row === undefined) {
    return {
      role,
      route: NOT_PRESENT,
      file: NOT_PRESENT,
      id: NOT_PRESENT,
      surface: NOT_PRESENT,
      stack: NOT_PRESENT,
      screenshot: NOT_MEASURED,
    };
  }
  return {
    role,
    route: row.path,
    file: row.page_file,
    id: row.id,
    surface: row.surface,
    stack: row.stack,
    screenshot: NOT_MEASURED,
  };
}

// 编号大小写敏感。整本账没有 media_result 时，才退到第一条 review。
export function selectRepresentatives(rows: readonly RepresentativeInput[]): PageRepresentative[] {
  const mediaResult = rows.find((row) => row.id === "media_result");
  return [
    record("Entry", rows.find((row) => row.id === "portal" || row.id === "login")),
    record("Home", rows.find((row) => row.id === "workspace")),
    record("List", rows.find((row) => row.id === "list")),
    record("Detail", rows.find((row) => row.id === "detail")),
    record("Main Action", rows.find((row) => row.id === "creation")),
    record("Result", mediaResult ?? rows.find((row) => row.id === "review")),
  ];
}

export const pageRepresentatives = selectRepresentatives(
  pageCensus().map((row) => ({
    path: row.path,
    page_file: row.page_file,
    id: row.id,
    surface: row.surface,
    stack: row.stack,
  })),
);
