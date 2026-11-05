// Playback speed is a real media setting, not a display-only control. These checks use the generated v5/v6 family
// so the selected rate has to survive a real source swap. The editable shortcut sheet has its own test because it
// must remain usable after the default bindings change.
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { LEGACY, PORTS } from "../lib/env.ts";
import { READY } from "../lib/review.ts";
import { startServer, type Server } from "../lib/server.ts";
import { family, type Family } from "../lib/work.ts";

test.describe.configure({ mode: "serial" });
test.skip(
  LEGACY,
  "new: playback speed and editable shortcuts are the current player",
);
test.skip(
  ({ browserName }) => browserName !== "chromium",
  "Chromium only: the finite media-rate check uses generated mp4",
);

let server: Server;
let fam: Family;
let context: BrowserContext;
let page: Page;

const rate = () =>
  page
    .locator("video")
    .evaluate((video: HTMLVideoElement) => video.playbackRate);
const media = () =>
  page.locator("video").evaluate((video: HTMLVideoElement) => ({
    paused: video.paused,
    time: video.currentTime,
  }));

async function open(): Promise<void> {
  await page.goto(server.url);
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await page.evaluate(() => (document.body as HTMLElement).focus());
}

async function pick(version: string): Promise<void> {
  await page.locator("#verBtn").click();
  await page
    .locator(`[data-part~="version-list"] [data-version="${version}"]`)
    .click();
  await expect
    .poll(() => page.evaluate(() => window.__review.view.shown))
    .toBe(version);
}

test.beforeAll(async ({ browser }) => {
  fam = family("playback-speed", ["v5", "v6"]);
  server = await startServer("playback-speed", fam.files.v6!, PORTS.keyboard);
  context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  await context.grantPermissions(["microphone"], {
    origin: server.url.replace(/\/$/, ""),
  });
  page = await context.newPage();
});

test.afterAll(async () => {
  await context?.close();
  await server?.stop();
});

test("A and D change the real media rate in quarter steps, clamp it, stop playback, and keep it across versions", async () => {
  await open();
  expect(await rate()).toBe(1);

  await page.keyboard.press("d");
  expect(await rate()).toBe(1.25);
  for (let i = 0; i < 3; i++) await page.keyboard.press("d");
  expect(await rate()).toBe(2);

  // At 2x, generated footage advances materially faster than wall time: this is not a label-only preference.
  const before = (await media()).time;
  await page
    .locator("video")
    .evaluate((video: HTMLVideoElement) => video.play());
  await page.waitForTimeout(700);
  expect((await media()).time).toBeGreaterThan(before + 0.8);

  for (let i = 0; i < 5; i++) await page.keyboard.press("d");
  expect(await rate()).toBe(3);
  await page.keyboard.press("d");
  expect(await rate()).toBe(3);
  for (let i = 0; i < 10; i++) await page.keyboard.press("a");
  expect(await rate()).toBe(0.5);
  await page.keyboard.press("a");
  expect(await rate()).toBe(0.5);

  for (let i = 0; i < 6; i++) await page.keyboard.press("d");
  expect(await rate()).toBe(2);
  await pick("v5");
  expect(await rate()).toBe(2);
  await pick("v6");
  expect(await rate()).toBe(2);

  await page
    .locator("video")
    .evaluate((video: HTMLVideoElement) => video.play());
  await page.keyboard.press("s");
  const stopped = await media();
  expect(stopped.paused).toBe(true);
  await page.waitForTimeout(250);
  expect((await media()).time).toBeCloseTo(stopped.time, 1);

  // Speed controls do not take over recording.
  await page.keyboard.press("r");
  await expect
    .poll(() => page.evaluate(() => !!window.__review.seg))
    .toBe(true);
  await page.keyboard.press("r");
  await expect
    .poll(() => page.evaluate(() => !!window.__review.seg))
    .toBe(false);
});

test("text controls leave playback shortcuts alone", async () => {
  await open();
  await page.evaluate(() => {
    for (const [tag, id] of [
      ["textarea", "speed-text"],
      ["select", "speed-select"],
      ["div", "speed-edit"],
    ] as const) {
      const element = document.createElement(tag);
      element.id = id;
      if (tag === "select") element.innerHTML = "<option>draft</option>";
      if (tag === "div") element.setAttribute("contenteditable", "true");
      document.body.append(element);
    }
  });
  await page.locator("video").evaluate((video: HTMLVideoElement) => {
    video.playbackRate = 1;
    video.play();
  });

  for (const target of ["#speed-text", "#speed-select", "#speed-edit"]) {
    await page.locator(target).focus();
    await page.keyboard.press("d");
    await page.keyboard.press("s");
    expect(await rate(), target).toBe(1);
    expect((await media()).paused, target).toBe(false);
  }
});

test("the shortcut sheet remaps playback keys, rejects conflicts, persists, and resets defaults", async () => {
  await open();
  await page.keyboard.press("?");
  const faster = page.getByLabel("Faster playback");
  await expect(faster).toBeVisible();
  await expect(faster).toHaveValue("D");
  await expect(page.getByLabel("Slower playback")).toHaveValue("A");
  await expect(page.getByLabel("Stop playback")).toHaveValue("S");

  await faster.fill("r");
  await expect(page.getByRole("alert")).toBeVisible();
  await faster.fill("u");
  await expect(page.getByRole("alert")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(faster).toHaveCount(0);

  await page.evaluate(() => {
    document.querySelector("video")!.playbackRate = 1;
    (document.body as HTMLElement).focus();
  });
  await page.keyboard.press("d");
  expect(await rate()).toBe(1);
  await page.keyboard.press("u");
  expect(await rate()).toBe(1.25);

  await page.reload();
  await page.waitForFunction(READY, null, { timeout: 20_000 });
  await page.keyboard.press("?");
  await expect(page.getByLabel("Faster playback")).toHaveValue("U");
  await page.getByRole("button", { name: /reset defaults/i }).click();
  await expect(page.getByLabel("Faster playback")).toHaveValue("D");

  // The sheet remains an entry surface: browser save/undo and Enter/Escape are not claimed as player bindings.
  await faster.focus();
  await page.keyboard.press(
    process.platform === "darwin" ? "Meta+s" : "Control+s",
  );
  await page.keyboard.press(
    process.platform === "darwin" ? "Meta+z" : "Control+z",
  );
  await page.keyboard.press("Enter");
  await page.keyboard.press("Escape");
  await expect(page.getByLabel("Faster playback")).toHaveCount(0);
});
