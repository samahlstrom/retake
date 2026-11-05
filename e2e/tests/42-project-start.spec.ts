// A person can begin reviewing their own footage without an editing agent or a versioned source filename.
import { expect, test } from "@playwright/test";
import { copyFileSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { LEGACY, PORTS, WORK } from "../lib/env.ts";
import { mediaLayout } from "../lib/fixtures.ts";
import { READY } from "../lib/review.ts";
import { startServer, type Server } from "../lib/server.ts";

test.skip(LEGACY, "local project creation belongs to the current application");
test.describe.configure({ mode: "serial" });

let server: Server;
let source: string;
let original: Buffer;

test.beforeAll(async () => {
  const root = join(WORK, "project-start");
  const library = join(root, "library");
  const config = join(root, "config");
  mkdirSync(library, { recursive: true });
  mkdirSync(join(config, "retake"), { recursive: true });
  writeFileSync(
    join(config, "retake", "config.toml"),
    'name = "Reviewer"\nopen_browser = false\nfind_agents = false\n',
  );
  source = join(root, "riverside-download.mp4");
  copyFileSync(mediaLayout(WORK).v6, source);
  original = readFileSync(source);
  server = await startServer("project-start", library, PORTS.setup, [], {
    env: { XDG_CONFIG_HOME: config },
  });
});

test.afterAll(async () => {
  await server?.stop();
});

test("empty Home imports an ordinary filename into a playable project without an agent", async ({
  page,
}) => {
  await page.goto(server.url);
  await expect(
    page.getByRole("button", { name: "New project", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Open existing project", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "New project", exact: true }).click();
  await page
    .getByLabel("Project name", { exact: true })
    .fill("Interview review");
  await page.locator('input[type="file"]').setInputFiles(source);
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click();
  await expect(page).toHaveURL(/\/p\/interview-review\/$/);
  await page.waitForFunction(READY, null, { timeout: 30_000 });
  await expect
    .poll(() =>
      page
        .locator("video")
        .evaluate((video: HTMLVideoElement) => video.readyState),
    )
    .toBeGreaterThanOrEqual(2);
  expect(readFileSync(source)).toEqual(original);
  const automation = await (
    await page.request.get(new URL("api/automation", page.url()).href)
  ).json();
  expect(automation.tasks ?? []).toEqual([]);
  await page.goto(server.url);
  await expect(
    page.getByRole("button", { name: "New project", exact: true }),
  ).toBeVisible();
  await expect(page.locator('[data-part~="home-grid"]')).toContainText(
    /interview/i,
  );
});

test("fresh Home shows its default Scripts editor enabled in Settings", async ({ page }) => {
  await page.goto(`${server.url}#settings`);
  const scripts = page.locator('[data-part~="settings-row"][data-ext="scripts"]');
  await expect(scripts.getByRole("switch")).toHaveAttribute("aria-checked", "true");
});

test("a blank project opens an editable brief without an agent", async ({ page }) => {
  await page.goto(server.url);
  await page.getByRole("button", { name: "New project", exact: true }).click();
  await page.getByLabel("Project name", { exact: true }).fill("Blank brief");
  await page.getByRole("button", { name: "Create project", exact: true }).click();
  await expect(page).toHaveURL(/\/p\/blank-brief\/$/);
  const brief = page.locator('.cm-content[contenteditable="true"]');
  await expect(brief).toBeVisible();
  await brief.fill("A short interview about the new product.");
  await expect(brief).toContainText("A short interview about the new product.");
  // The app saves the starting brief; no editing agent is needed to create or write it.
  await expect.poll(async () => {
    const res = await page.request.get(new URL("api/automation", page.url()).href);
    return (await res.json()).tasks ?? [];
  }).toEqual([]);
  await page.waitForTimeout(1000);
  await page.reload();
  await expect(brief).toContainText("A short interview about the new product.");
});

test("shared viewer Home does not expose local project creation", async ({
  page,
}) => {
  await page.route("**/api/home", async (route) => {
    const response = await route.fetch();
    const body = await response.json();
    await route.fulfill({ response, json: { ...body, role: "watch" } });
  });
  await page.goto(server.url);
  await expect(page.locator('[data-part~="home-grid"]')).toBeVisible();
  await expect(
    page.getByRole("button", { name: "New project", exact: true }),
  ).toHaveCount(0);
  await expect(
    page.getByRole("link", { name: "Open existing project", exact: true }),
  ).toHaveCount(0);
});
