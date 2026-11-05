// Custom shortcuts apply to existing player actions as well as the newly added speed controls.
import { expect, test } from "@playwright/test";
import { LEGACY, PORTS } from "../lib/env.ts";
import { READY } from "../lib/review.ts";
import { startServer, type Server } from "../lib/server.ts";
import { family } from "../lib/work.ts";

test.skip(
  LEGACY,
  "editable player shortcuts belong to the current application",
);
let server: Server;
test.beforeAll(async () => {
  const project = family("custom-player-keys", ["v6"]);
  server = await startServer(
    "custom-player-keys",
    project.files.v6!,
    PORTS.keyboard,
  );
});
test.afterAll(async () => {
  await server?.stop();
});

test("Record can be remapped and reset without changing browser entry keys", async ({
  browser,
}) => {
  const context = await browser.newContext();
  await context.grantPermissions(["microphone"], {
    origin: server.url.replace(/\/$/, ""),
  });
  const page = await context.newPage();
  try {
    await page.goto(server.url);
    await page.waitForFunction(READY, null, { timeout: 20_000 });
    await page.keyboard.press("?");
    const recordKey = page.getByRole("textbox", { name: /^Record/ });
    await expect(recordKey).toHaveValue("R");
    await recordKey.fill("j");
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-part="keys-sheet"]')).toHaveCount(0);
    await page.keyboard.press("r");
    expect(await page.evaluate(() => !!window.__review.seg)).toBe(false);
    await page.keyboard.press("j");
    await expect
      .poll(() => page.evaluate(() => !!window.__review.seg))
      .toBe(true);
    await page.keyboard.press("j");
    await expect
      .poll(() => page.evaluate(() => !!window.__review.seg))
      .toBe(false);
    await page.reload();
    await page.waitForFunction(READY, null, { timeout: 20_000 });
    await page.keyboard.press("?");
    await expect(recordKey).toHaveValue("J");
    await page.getByRole("button", { name: /Reset defaults/i }).click();
    await expect(recordKey).toHaveValue("R");
    await page.keyboard.press("Escape");
    await expect(page.locator('[data-part="keys-sheet"]')).toHaveCount(0);
    await page.keyboard.press("r");
    await expect
      .poll(() => page.evaluate(() => !!window.__review.seg))
      .toBe(true);
    await page.keyboard.press("r");
    await expect
      .poll(() => page.evaluate(() => !!window.__review.seg))
      .toBe(false);
  } finally {
    await context.close();
  }
});
