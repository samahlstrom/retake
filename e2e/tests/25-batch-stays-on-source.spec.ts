// A batch may create a target while the reviewer keeps watching its source. Two pages receive the same task state;
// neither follows it. The target opens only through the explicit View action after every comment is resolved.
import { expect, test, type BrowserContext, type Page } from "@playwright/test";
import { writeFileSync } from "node:fs";
import { join } from "node:path";
import { LEGACY, PORTS } from "../lib/env.ts";
import { startProgress, type Fixture } from "../lib/progress.ts";
import { READY } from "../lib/review.ts";

test.describe.configure({ mode: "serial" });
test.skip(
  LEGACY,
  "new: batches and explicit target navigation are the current player",
);
test.skip(
  ({ browserName }) => browserName !== "chromium",
  "Chromium only: the fake editing agent drives generated mp4",
);

let fixture: Fixture | undefined;

const shown = (page: Page) => page.evaluate(() => window.__review.view.shown);
const automation = (page: Page) =>
  page.request.get(new URL("api/automation", fixture!.server.url).href).then(
    (response) =>
      response.json() as Promise<{
        tasks: { state: string; marked?: unknown[] }[];
      }>,
  );

async function addComment(
  page: Page,
  time: number,
  text: string,
): Promise<void> {
  const response = await page.request.post(
    new URL("api/notes", fixture!.server.url).href,
    {
      data: { version: "v1", t: time, text },
    },
  );
  expect(response.status()).toBe(201);
}

async function open(page: Page): Promise<void> {
  await page.goto(fixture!.server.url);
  await page.waitForFunction(READY, null, { timeout: 30_000 });
  await expect.poll(() => shown(page)).toBe("v1");
}

function release(name: string): void {
  writeFileSync(join(fixture!.gates, name), "");
}

test.afterEach(async () => {
  if (fixture) {
    for (const gate of ["start", "work-1", "work-2", "work-3", "report"])
      release(gate);
    await fixture.server.stop();
    fixture = undefined;
  }
});

test("Send leaves both reviewers on v1 until every comment is done and they choose View v2", async ({
  browser,
}) => {
  fixture = await startProgress("stay-on-source", PORTS.progress);
  const source: BrowserContext = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const receiving: BrowserContext = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  const page = await source.newPage();
  const other = await receiving.newPage();
  try {
    await open(page);
    for (const [time, text] of [
      [1, "Trim this beat"],
      [4, "Keep the close-up"],
      [7, "Raise the title"],
      [10, "Soften this cut"],
      [13, "Hold the ending"],
    ] as const)
      await addComment(page, time, text);
    await expect(page.locator("#side .item")).toHaveCount(5);
    await open(other); // The second reviewer is already on v1 when the task state arrives.

    await page.locator('[data-part~="send"]:visible').first().click();
    await expect
      .poll(
        async () => (await automation(page)).tasks.map((task) => task.state),
        { timeout: 60_000 },
      )
      .toEqual(["running"]);
    await expect.poll(() => shown(page), { timeout: 15_000 }).toBe("v1");
    await expect.poll(() => shown(other), { timeout: 15_000 }).toBe("v1");
    await expect(page.locator("#side .status")).toHaveText(
      Array(5).fill("Working"),
    );
    await expect(
      page.getByRole("button", { name: "View v2", exact: true }),
    ).toHaveCount(0);

    // The target is still available for deliberate early inspection through the existing version picker.
    await page.locator("#verBtn").click();
    await expect(
      page.locator('[data-part~="version-list"] [data-version^="v2"]'),
    ).toBeVisible();
    await page.keyboard.press("Escape");

    await page.reload();
    await page.waitForFunction(READY, null, { timeout: 30_000 });
    await expect.poll(() => shown(page), { timeout: 15_000 }).toBe("v1");
    await expect(page.locator("#side .status")).toHaveText(
      Array(5).fill("Working"),
    );

    release("start");
    for (const [index, gate] of ["work-1", "work-2", "work-3"].entries()) {
      release(gate);
      await expect
        .poll(
          async () => (await automation(page)).tasks[0]?.marked?.length ?? 0,
          { timeout: 45_000 },
        )
        .toBe(index + 1);
      await expect.poll(() => shown(page), { timeout: 15_000 }).toBe("v1");
    }
    release("report");
    await expect
      .poll(
        async () => (await automation(page)).tasks.map((task) => task.state),
        { timeout: 60_000 },
      )
      .toEqual(["completed"]);
    await expect.poll(() => shown(page), { timeout: 15_000 }).toBe("v1");
    await page.getByRole("tab", { name: "Done 5", exact: true }).click();
    await expect(page.locator("#side .status")).toHaveText(
      Array(5).fill("Done"),
    );

    const view = page.getByRole("button", { name: "View v2", exact: true });
    await expect(view).toBeVisible();
    await view.click();
    await expect.poll(() => shown(page), { timeout: 15_000 }).toBe("v2");
  } finally {
    await source.close();
    await receiving.close();
  }
});
