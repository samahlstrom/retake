// The eraser (new): E or the eraser button makes the pen an eraser. While recording, a swipe over a line of this take
// removes that whole line; Z brings back the last erased line; a pen color switches back to the pen. Not recording,
// it erases nothing, like the pen draws nothing. Erased lines are left out of strokes.json, and events.json says what
// happened (erase / unerase), so the take still validates.
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { LEGACY, PORTS } from "../lib/env.ts";
import { line, Review, sleep } from "../lib/review.ts";
import { validateTakeDir } from "../lib/schema.ts";
import { startServer, type Server } from "../lib/server.ts";
import { family, takeDirs, type Family } from "../lib/work.ts";

test.describe.configure({ mode: "serial" });
test.skip(LEGACY, "new: the legacy player has no eraser");

let fam: Family;
let server: Server;
let context: BrowserContext;
let page: Page;
let ui: Review;
const pageErrors: string[] = [];

/** This take's strokes: id, and whether erased or undone. */
const strokes = () =>
  page.evaluate(() =>
    window.__review.take.strokes.map((s) => ({ id: s.id, undone: !!s.undone })),
  );
const live = async () =>
  (await strokes()).filter((s) => !s.undone).map((s) => s.id);

test.beforeAll(async ({ browser }) => {
  fam = family("eraser", ["v6"]);
  server = await startServer("eraser", fam.files.v6!, PORTS.eraser);
  context = await browser.newContext({
    viewport: { width: 1920, height: 1080 },
  });
  page = await context.newPage();
  page.on("pageerror", (e) => pageErrors.push(`${e.name}: ${e.message}`));
  ui = new Review(page);
  await ui.open(server.url);
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test("the eraser removes the line it crosses, only while recording; Z brings it back", async () => {
  await page.evaluate(() => {
    const v = document.querySelector("video")!;
    v.pause();
    v.currentTime = 5;
  });
  await sleep(300);
  await ui.key("r");
  await ui.waitFor(() => !!window.__review.seg, 8000);
  await ui.drag(line([0.2, 0.3], [0.5, 0.3])); // id 1
  await ui.drag(line([0.2, 0.6], [0.5, 0.6])); // id 2
  expect(await live()).toEqual([1, 2]);

  // E, then a swipe down through line 1 only.
  await ui.key("e");
  expect(
    await page.evaluate(() =>
      document.querySelector("#ink")!.classList.contains("erasing"),
    ),
  ).toBe(true);
  await ui.drag(line([0.35, 0.22], [0.35, 0.4]));
  expect(await live()).toEqual([2]);

  // Z brings line 1 back.
  await ui.key("z");
  expect(await live()).toEqual([1, 2]);

  // The eraser button works like E; a swipe through both lines takes both.
  await ui.click("#eraser"); // off
  await ui.click("#eraser"); // on
  expect(await page.locator("#eraser").getAttribute("aria-pressed")).toBe(
    "true",
  );
  await ui.drag(line([0.3, 0.2], [0.3, 0.7]));
  expect(await live()).toEqual([]);
  await ui.key("z"); // brings back the later erase first
  await ui.key("z");
  expect((await live()).sort()).toEqual([1, 2]);

  // A pen color switches back to the pen: a drag draws again.
  await ui.key("2");
  expect(await page.locator("#eraser").getAttribute("aria-pressed")).toBe(
    "false",
  );
  await ui.drag(line([0.6, 0.3], [0.8, 0.3])); // id 3
  expect(await live()).toEqual([1, 2, 3]);

  // Erase line 1 for good, then stop recording: the eraser no longer erases.
  await ui.key("e");
  await ui.drag(line([0.35, 0.22], [0.35, 0.4]));
  expect(await live()).toEqual([2, 3]);
  await ui.key("r");
  await ui.waitFor(() => !window.__review.seg, 8000);
  await ui.drag(line([0.3, 0.5], [0.3, 0.7])); // over line 2, not recording
  expect(await live()).toEqual([2, 3]);
});

test("Done saves only the remaining lines, and the events tell the story", async () => {
  await ui.click("#done");
  await ui.waitFor(
    () => window.__review.take.closed && !window.__review.take.saving,
    15_000,
  );
  const dirs = takeDirs(join(fam.dir, "v6", "clip_v6.review"));
  expect(dirs).toHaveLength(1);
  validateTakeDir(dirs[0]!);
  const saved = JSON.parse(
    readFileSync(join(dirs[0]!, "strokes.json"), "utf8"),
  ) as { id: number }[];
  expect(saved.map((s) => s.id)).toEqual([2, 3]);
  const events = JSON.parse(
    readFileSync(join(dirs[0]!, "events.json"), "utf8"),
  ) as { event: string; id?: number }[];
  expect(
    events
      .filter((e) => e.event === "erase" || e.event === "unerase")
      .map((e) => `${e.event} ${e.id}`),
  ).toEqual([
    "erase 1",
    "unerase 1",
    "erase 1",
    "erase 2",
    "unerase 2",
    "unerase 1",
    "erase 1",
  ]);
});

test("no uncaught page errors", async () => {
  expect(pageErrors).toEqual([]);
});
