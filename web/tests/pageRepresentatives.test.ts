import { describe, expect, it } from "vitest";
import { pageCensus } from "@/lib/pageCensus";
import { pageRepresentatives, selectRepresentatives } from "@/lib/pageRepresentatives";
import type { PageRepresentative, RepresentativeInput } from "@/lib/pageRepresentatives";

function row(id: string, path: string, page_file: string, surface = "work", stack = "next"): RepresentativeInput {
  return { path, page_file, id, surface, stack };
}

describe("page representatives", () => {
  it("locks fixture choices to object literals", () => {
    expect(
      selectRepresentatives([
        row("portal", "/z-door", "src/app/z-door/page.tsx", "portal"),
        row("portal", "/a-door", "src/app/a-door/page.tsx", "portal"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "/z-door",
        file: "src/app/z-door/page.tsx",
        id: "portal",
        surface: "portal",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
    ]);

    expect(
      selectRepresentatives([
        row("login", "/sign-in", "src/app/sign-in/page.tsx", "portal"),
        row("portal", "/door", "src/app/door/page.tsx", "portal"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "/sign-in",
        file: "src/app/sign-in/page.tsx",
        id: "login",
        surface: "portal",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
    ]);

    expect(
      selectRepresentatives([
        row("review", "/early-review", "src/app/early-review/page.tsx"),
        row("media_result", "/first-media", "src/app/first-media/page.tsx"),
        row("review", "/late-review", "src/app/late-review/page.tsx"),
        row("media_result", "/second-media", "src/app/second-media/page.tsx"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "/first-media",
        file: "src/app/first-media/page.tsx",
        id: "media_result",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
    ]);

    expect(
      selectRepresentatives([
        row("list", "/leads", "src/app/(shell)/leads/page.tsx"),
        row("review", "/attribution", "src/app/(shell)/attribution/page.tsx"),
        row("review", "/intent", "src/app/(shell)/intent/page.tsx"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "/leads",
        file: "src/app/(shell)/leads/page.tsx",
        id: "list",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "/attribution",
        file: "src/app/(shell)/attribution/page.tsx",
        id: "review",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
    ]);

    expect(
      selectRepresentatives([
        row("workspace", "/", "src/app/(shell)/page.tsx"),
        row("list", "/leads", "src/app/(shell)/leads/page.tsx"),
        row("detail", "/leads/[id]", "src/app/(shell)/leads/[id]/page.tsx"),
        row("media_result", "/media", "src/app/media/page.tsx"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "/",
        file: "src/app/(shell)/page.tsx",
        id: "workspace",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "/leads",
        file: "src/app/(shell)/leads/page.tsx",
        id: "list",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "/leads/[id]",
        file: "src/app/(shell)/leads/[id]/page.tsx",
        id: "detail",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "/media",
        file: "src/app/media/page.tsx",
        id: "media_result",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
    ]);

    expect(
      selectRepresentatives([
        row("Portal", "/capital", "src/app/capital/page.tsx", "portal"),
        row("workspace", "/", "src/app/(shell)/page.tsx"),
      ]),
    ).toEqual([
      {
        role: "Entry",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Home",
        route: "/",
        file: "src/app/(shell)/page.tsx",
        id: "workspace",
        surface: "work",
        stack: "next",
        screenshot: "not_measured",
      },
      {
        role: "List",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Detail",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Main Action",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
      {
        role: "Result",
        route: "not_present",
        file: "not_present",
        id: "not_present",
        surface: "not_present",
        stack: "not_present",
        screenshot: "not_measured",
      },
    ]);
  });

  // 与生产选择器分开写的一次手走。期望不调用 selectRepresentatives。
  it("matches a handwritten walk of the real census", () => {
    const rows = pageCensus().map((row) => ({
      path: row.path,
      page_file: row.page_file,
      id: row.id,
      surface: row.surface,
      stack: row.stack,
    }));

    function chosen(match: (id: string) => boolean) {
      return rows.find((row) => match(row.id));
    }

    function noted(
      role: PageRepresentative["role"],
      hit: (typeof rows)[number] | undefined,
    ): PageRepresentative {
      if (hit === undefined) {
        return {
          role,
          route: "not_present",
          file: "not_present",
          id: "not_present",
          surface: "not_present",
          stack: "not_present",
          screenshot: "not_measured",
        };
      }
      return {
        role,
        route: hit.path,
        file: hit.page_file,
        id: hit.id,
        surface: hit.surface,
        stack: hit.stack,
        screenshot: "not_measured",
      };
    }

    const mediaResult = chosen((id) => id === "media_result");
    const expected = [
      noted("Entry", chosen((id) => id === "portal" || id === "login")),
      noted("Home", chosen((id) => id === "workspace")),
      noted("List", chosen((id) => id === "list")),
      noted("Detail", chosen((id) => id === "detail")),
      noted("Main Action", chosen((id) => id === "creation")),
      noted("Result", mediaResult ?? chosen((id) => id === "review")),
    ];

    expect(pageRepresentatives).toEqual(expected);
    expect(pageRepresentatives.map((item) => item.role)).toEqual([
      "Entry",
      "Home",
      "List",
      "Detail",
      "Main Action",
      "Result",
    ]);
  });
});
