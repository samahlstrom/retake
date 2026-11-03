// Comments belong to the version they were made on (spec/notes.md, "Comments belong to their version"). A family of
// three versions, v1 v2 v3, with comments on v1 and v3: v3 (the newest) shows only its own comment and marker, the
// comments' version menu says what's still open on v1 (and where each version's comments go) and goes there, it and
// the bar's picker switch versions and take the comments and markers along, and Send counts and sends the shown
// version's comments only.
//
// The page reads the family's comments once (`scope=video`), so GET /api/notes is mocked with every version's items
// tagged with their `version`, as the server answers it; the versions and their videos are real.
import { copyFileSync, linkSync, mkdirSync, rmSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page, type Route } from "@playwright/test";
import { LEGACY, PORTS, WORK } from "../lib/env.ts";
import { mediaLayout } from "../lib/fixtures.ts";
import { READY } from "../lib/review.ts";
import { startServer, type Server } from "../lib/server.ts";

test.skip(LEGACY, "versions in the comments panel are the current player");
let server: Server;
const json = (r: Route, body: unknown) =>
  r.fulfill({ contentType: "application/json", body: JSON.stringify(body) });
/** The one version menu (spec/notes.md, "The version menu"): the bar's, on a phone in the controls row. */
const versionMenu = (page: Page) =>
  page.locator('[data-part~="version-picker"]:visible');
/** Opens it and lists its rows: [version, its one detail, on screen]. */
async function menuEntries(page: Page): Promise<(string | boolean)[][]> {
  await versionMenu(page).click();
  const rows = page.locator('[data-part~="version-list"] [data-version]');
  await expect(rows.first()).toBeVisible();
  const out = await rows.evaluateAll((els) =>
    els.map((e) => [
      (e as HTMLElement).dataset.version!,
      e.querySelector(".d")?.textContent?.trim() ?? "",
      e.getAttribute("aria-current") === "true",
    ]),
  );
  await page.keyboard.press("Escape");
  return out;
}
/** Picks a version from it. */
async function menuPick(page: Page, version: string): Promise<void> {
  await versionMenu(page).click();
  await page
    .locator(`[data-part~="version-list"] [data-version="${version}"]`)
    .click();
}

const items = [
  {
    id: 1,
    version: "v1",
    kind: "note",
    status: "open",
    t0: 4,
    t1: 5,
    text: "v1: the intro runs long",
    by: "Sam",
    take: "take-v1",
    seg: "S1",
    drawings: ["red"],
  },
  {
    id: 2,
    version: "v1",
    kind: "note",
    status: "open",
    t0: 30,
    t1: 31,
    text: "v1: the music is too loud",
    by: "Sam",
  },
  {
    id: 3,
    version: "v1",
    kind: "note",
    status: "answered",
    t0: 12,
    t1: 13,
    text: "v1: trim the pause",
    by: "Sam",
    change: "Pause trimmed",
    answered_in: "v2",
  },
  {
    id: 4,
    version: "v3",
    kind: "note",
    status: "open",
    t0: 20,
    t1: 21,
    text: "v3: the title is too small",
    by: "Sam",
  },
];

test.beforeAll(async () => {
  // v1, v2, v3 from the generated 40 s clips, hardlinked (copied across filesystems).
  const dir = join(WORK, "versions");
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  const m = mediaLayout(WORK);
  for (const [label, src] of [
    ["v1", m.v5],
    ["v2", m.v6],
    ["v3", m.v5],
  ] as const) {
    const dst = join(dir, `shop_${label}.mp4`); // its own stem: media/ beside it holds the clip_ family
    try {
      linkSync(src, dst);
    } catch {
      copyFileSync(src, dst);
    }
  }
  server = await startServer(
    "versions",
    join(dir, "shop_v3.mp4"),
    PORTS.sidebar,
  );
});
test.afterAll(async () => {
  await server?.stop();
});

/** The mocked routes; returns what the page asked for and sent. */
async function mock(page: Page) {
  const seen = {
    notes: [] as string[],
    history: [] as string[],
    submitted: [] as number[][],
  };
  const tasks: Record<string, unknown>[] = [];
  await page.route("**/api/notes?*", (r) => {
    const u = new URL(r.request().url());
    seen.notes.push(u.search);
    const v = u.searchParams.get("v") ?? "";
    return json(r, {
      version: v,
      newest: v === "v3",
      scope: "video",
      jobs: [],
      items: items.map((n) => ({
        ...n,
        work: tasks.find((t) => (t.notes as number[]).includes(n.id)),
      })),
    });
  });
  await page.route("**/api/history?*", (r) => {
    const u = new URL(r.request().url());
    seen.history.push(u.search);
    const v = u.searchParams.get("v");
    // v1's take has a red line at 0:04; nothing else has drawings.
    return json(r, {
      label: v,
      history:
        v === "v1"
          ? [
              {
                id: "take-v1",
                inherited_from: null,
                transcribed: true,
                voice: {},
                strokes: [
                  {
                    id: 1,
                    video_t: 4,
                    color: "red",
                    points: [
                      [0.3, 0.3],
                      [0.6, 0.6],
                    ],
                  },
                ],
              },
            ]
          : [],
    });
  });
  await page.route("**/api/events*", (r) =>
    r.fulfill({
      status: 200,
      contentType: "text/event-stream",
      body: "retry: 60000\n\n",
    }),
  );
  await page.route("**/api/automation", (r) =>
    json(r, {
      local: false,
      config: {
        worker: "machine",
        agent: "claude",
        fallback: "",
        enabled: false,
      },
      workers: [],
      tasks,
      renders: [],
      sending: [],
    }),
  );
  await page.route("**/api/automation/tasks", (r) => {
    const body = JSON.parse(r.request().postData()!) as { notes: number[] };
    seen.submitted.push(body.notes);
    for (const id of body.notes)
      tasks.push({
        id: `t${id}`,
        notes: [id],
        version: "v1",
        state: "queued",
        agent: "",
        worker: "",
        attempt: 0,
        events: [],
      });
    return json(r, { ok: true });
  });
  await page.route("**/api/connect", (r) =>
    json(r, {
      command: "retake connect https://review.example.com --token rk_test",
    }),
  );
  await page.goto(server.url);
  await page.waitForFunction(READY);
  return seen;
}

const shown = (page: Page) => page.evaluate(() => window.__review.view.shown);
const markers = (page: Page) =>
  page
    .locator(".comment-marker")
    .evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset.comment));
const strokes = (page: Page) =>
  page.evaluate(() => window.__review.hist.strokes.length);

test("the newest version shows its own comments; the line goes to the earlier ones; the strip switches back", async ({
  page,
}) => {
  const seen = await mock(page);
  await expect.poll(() => shown(page)).toBe("v3");
  // v3: its one comment, its one marker, nothing of v1's.
  await expect(page.locator("#side .item")).toHaveCount(1);
  await expect(page.locator("#side .item")).toContainText(
    "v3: the title is too small",
  );
  await expect(
    page.getByRole("tab", { name: "To do 1", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("tab", { name: "Done 0", exact: true }),
  ).toBeVisible();
  expect(await markers(page)).toEqual(["note:4"]);
  await expect(page.locator("#done")).toHaveText("Send");
  await expect(page.locator("#done")).toHaveAttribute(
    "aria-label",
    "Send 1 comment to the agent",
  );
  expect(await strokes(page)).toBe(0); // v1's drawing never shows on v3
  // The version menu: v3 v2 v1, newest first, v3 on screen; one detail each, what's still to do, else the length.
  await expect(versionMenu(page)).toHaveText("v3");
  expect(await menuEntries(page)).toEqual([
    ["v3", "1 to do", true],
    ["v2", "0:40", false],
    ["v1", "2 to do", false],
  ]);
  // Its dot and title say what's still open earlier.
  await expect(versionMenu(page)).toHaveAttribute(
    "title",
    /Open on earlier versions: v1 · 2/,
  );
  await expect(versionMenu(page)).toHaveClass(/\bearlier\b/);

  await menuPick(page, "v1");
  await expect.poll(() => shown(page)).toBe("v1");
  await expect(page.locator("#side .item")).toHaveCount(2);
  await expect(page.locator("#side .item").first()).toContainText(
    "v1: the intro runs long",
  );
  await expect(
    page.getByRole("tab", { name: "To do 2", exact: true }),
  ).toBeVisible();
  expect(await markers(page)).toEqual(["note:1", "note:2"]);
  await expect(versionMenu(page)).not.toHaveClass(/\bearlier\b/); // only on the newest version
  await expect(page.locator("#done")).toHaveAttribute(
    "aria-label",
    "Send 2 comments to the agent",
  );
  await expect.poll(() => strokes(page)).toBe(1); // v1's own drawing is back on v1
  // Its markers sit at its own times on its own clock (0:04 and 0:30 of 40 s).
  const tl = (await page.locator("#timeline").boundingBox())!;
  const at = await Promise.all(
    (await page.locator(".comment-marker").all()).map(async (m) => {
      const b = (await m.boundingBox())!;
      return (b.x + b.width / 2 - tl.x - 8) / (tl.width - 16);
    }),
  );
  expect(at[0]).toBeCloseTo(0.1, 1);
  expect(at[1]).toBeCloseTo(0.75, 1);
  await page.getByRole("tab", { name: "Done 1", exact: true }).click();
  await expect(page.locator("#side .item")).toContainText("Pause trimmed");
  expect(await markers(page)).toEqual(["note:3"]);
  await page.getByRole("tab", { name: /^To do/ }).click();

  await menuPick(page, "v3");
  await expect.poll(() => shown(page)).toBe("v3");
  await expect(page.locator("#side .item")).toHaveCount(1);
  expect(await markers(page)).toEqual(["note:4"]);
  // The page reads the family once per version and each version's own history, never another version's drawings.
  expect(seen.notes.every((q) => q.includes("scope=video"))).toBe(true);
  expect(seen.history.some((q) => q.includes("scope="))).toBe(false);
});

test("the bar's picker takes the comments and the markers along", async ({
  page,
}) => {
  await mock(page);
  await expect.poll(() => shown(page)).toBe("v3");
  const pick = async (label: string) => {
    await page.locator("#verBtn").click();
    await page.locator(`#verList [data-ver="${label}"]`).click();
    await expect.poll(() => shown(page)).toBe(label);
  };
  await pick("v2");
  await expect(page.locator("#side .item")).toHaveCount(0);
  await expect(page.locator("#side .empty")).toContainText("No comments yet");
  expect(await markers(page)).toEqual([]);
  await expect(versionMenu(page)).toHaveText("v2");
  await expect(versionMenu(page)).not.toHaveClass(/\bearlier\b/);
  await pick("v1");
  await expect(page.locator("#side .item")).toHaveCount(2);
  expect(await markers(page)).toEqual(["note:1", "note:2"]);
  await pick("v3");
  await expect(page.locator("#side .item")).toHaveCount(1);
  expect(await markers(page)).toEqual(["note:4"]);
  await expect(versionMenu(page)).toHaveAttribute(
    "title",
    /Open on earlier versions: v1 · 2/,
  );
});

test("Send sends the shown version's comments; an older version's go from there", async ({
  page,
}) => {
  const seen = await mock(page);
  await expect(page.locator("#done")).toHaveAttribute(
    "aria-label",
    "Send 1 comment to the agent",
  );
  await page.locator("#done").click();
  await expect.poll(() => seen.submitted).toEqual([[4]]);
  await expect(page.locator("#toast")).toContainText("Sent to v4.");
  await menuPick(page, "v1");
  await expect.poll(() => shown(page)).toBe("v1");
  await expect(page.locator("#done")).toHaveAttribute(
    "aria-label",
    "Send 2 comments to the agent",
  );
  await page.locator("#done").click();
  await expect.poll(() => seen.submitted).toEqual([[4], [1, 2]]);
});

test("a single version has no version menu: nothing to choose", async ({
  page,
}) => {
  await page.route("**/api/info", async (r) => {
    const real = (await (await r.fetch()).json()) as {
      versions: { label: string }[];
    };
    return json(r, {
      ...real,
      downloads: false,
      versions: real.versions.filter((v) => v.label === "v3"),
    });
  });
  await mock(page);
  await expect(page.locator("#side .item")).toHaveCount(1);
  await expect(page.locator('[data-part~="version-picker"]')).toHaveCount(0);
});

test("a single version with downloads on: the menu is just Download", async ({
  page,
}) => {
  await page.route("**/api/info", async (r) => {
    const real = (await (await r.fetch()).json()) as {
      versions: { label: string }[];
    };
    return json(r, {
      ...real,
      downloads: true,
      versions: real.versions.filter((v) => v.label === "v3"),
    });
  });
  await mock(page);
  await versionMenu(page).click();
  const list = page.locator('[data-part~="version-list"]');
  await expect(list).toBeVisible();
  await expect(list.locator("[data-version]")).toHaveCount(0);
  await expect(list).toContainText("Download v3 (MP4)");
});

test("phone: the version menu fits the screen", async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await mock(page);
  await expect(versionMenu(page)).toBeVisible();
  await expect(
    page.locator('#pbar [data-part~="version-picker"]'),
  ).toBeVisible(); // in the controls row
  await versionMenu(page).click();
  const list = page.locator('[data-part~="version-list"]');
  await expect(list).toBeVisible();
  const box = (await list.boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(390);
  await list.locator('[data-version="v1"]').click();
  await expect.poll(() => shown(page)).toBe("v1");
  await expect(page.locator("#side .item")).toHaveCount(2);
});
