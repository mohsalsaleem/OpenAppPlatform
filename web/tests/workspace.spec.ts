import { test, expect } from "@playwright/test";

test("authentication, application setup, release confirmation, and target discovery", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByLabel("Platform access token").fill("invalid");
  await page.getByRole("button", { name: "Connect to workspace" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "valid platform access token",
  );
  await page
    .getByLabel("Platform access token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Your applications" }),
  ).toBeVisible();
  await page.getByRole("link", { name: "New application" }).click();
  const name = `browser-${Date.now()}`;
  await page.getByLabel("Application name", { exact: true }).fill(name);
  await page.getByRole("button", { name: "Add component" }).click();
  await page.getByLabel("Component name", { exact: true }).nth(1).fill("api");
  await page.getByRole("button", { name: "Create application" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await expect(page.getByText("Ready for your first deployment")).toBeVisible();
  await expect(page.getByText("api", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Deploy application" }).click();
  await expect(page.getByRole("dialog")).toContainText("can cause downtime");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page.getByRole("button", { name: "Deploy application" }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("tab", { name: "Configuration" }).click();
  await expect(page.locator("pre")).toContainText("standard");
  await page.screenshot({
    path: "../.local/ui-configuration.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "Deployment targets" }).click();
  await page.getByRole("button", { name: "Inspect resources" }).click();
  await expect(
    page.getByText(/resources in this target environment/),
  ).toBeVisible();
  await page.getByRole("link", { name: "Applications", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Your applications" }),
  ).toBeVisible();
  await page.screenshot({ path: "../.local/ui-desktop.png", fullPage: true });
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({ path: "../.local/ui-mobile.png", fullPage: true });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  expect(errors).toEqual([]);
});
