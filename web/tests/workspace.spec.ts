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
  await page.getByLabel("Environment variables for web").fill('{"unfinished":');
  await expect(
    page.getByRole("button", { name: "Create application" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Remove component 1" }).click();
  await expect(
    page.getByRole("button", { name: "Create application" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Add component" }).click();
  await page.getByLabel("Component name", { exact: true }).nth(1).fill("web");
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
  await expect(
    page.getByRole("heading", { name: "Runtime configuration" }),
  ).toBeVisible();
  await page.getByLabel("Container image for web").fill("nginx:1.28-alpine");
  await page.getByRole("button", { name: "Save configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await page.getByText("View application definition").click();
  await expect(page.locator("pre")).toContainText("nginx:1.28-alpine");
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

test("deploy, inspect live instances, read logs, and update a Docker application", async ({
  page,
}) => {
  test.setTimeout(90000);
  test.skip(
    !process.env.OAP_TEST_DOCKER_IMAGE,
    "Requires the isolated Docker test environment",
  );
  async function deployAndWait() {
    await page.getByRole("button", { name: "Deploy application" }).click();
    const queued = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.url().endsWith("/deployments"),
    );
    await page.getByRole("button", { name: "Deploy now" }).click();
    const response = await queued;
    expect(response.status()).toBe(202);
    const release = await response.json();
    await expect
      .poll(
        async () => {
          const current = await page.request.get(
            `/api/v1/deployments/${release.id}`,
            {
              headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` },
            },
          );
          expect(current.ok()).toBeTruthy();
          return (await current.json()).state;
        },
        { timeout: 60000 },
      )
      .toBe("succeeded");
    await expect(
      page.getByRole("button", { name: "Deploy application" }),
    ).toBeEnabled();
  }
  await page.goto("/");
  await page
    .getByLabel("Platform access token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to workspace" }).click();
  await page.getByRole("link", { name: "New application" }).click();
  await page
    .getByLabel("Application name", { exact: true })
    .fill(`browser-live-${Date.now()}`);
  await page
    .getByLabel("Container image", { exact: true })
    .fill(process.env.OAP_TEST_DOCKER_IMAGE!);
  await page.getByLabel("Internal port", { exact: true }).fill("8080");
  await page
    .getByLabel("Environment variables for web")
    .fill('{"OAP_TEST_MESSAGE":');
  await expect(
    page.getByRole("button", { name: "Create application" }),
  ).toBeDisabled();
  await page
    .getByLabel("Environment variables for web")
    .fill('{"OAP_TEST_MESSAGE":"browser-v1"}');
  await page
    .getByLabel("Service connections for web")
    .fill('{"UPSTREAM_URL":"web"}');
  await page.getByRole("button", { name: "Create application" }).click();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  const live = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Live instances" }) });
  await expect(live).toContainText("running:healthy", { timeout: 15000 });
  await page.getByRole("button", { name: "View web instance 1 logs" }).click();
  await expect(page.locator(".log-output")).toContainText("fixture", {
    timeout: 5000,
  });
  await page.getByRole("tab", { name: "Configuration" }).click();
  await page
    .getByLabel("Container image for web")
    .fill(process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!);
  await page.getByLabel("Internal port for web").fill("8025");
  await page
    .getByLabel("Environment variables for web")
    .fill('{"OAP_TEST_MESSAGE":"browser-v2"}');
  await page.getByRole("button", { name: "Save configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(live).toContainText("running:healthy", { timeout: 15000 });
  await expect(page.locator(".image-ref")).toContainText(
    process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!,
  );
  await expect
    .poll(async () => {
      const applicationId = page.url().split("/").at(-1);
      const response = await page.request.get(
        `/api/v1/applications/${applicationId}/instances`,
        { headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` } },
      );
      expect(response.ok()).toBeTruthy();
      return (await response.json())[0].resource.image;
    })
    .toBe(process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!);
  await expect(page.locator(".release-banner")).toContainText("succeeded");
  await page
    .getByRole("button", { name: "Restart web / 1", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "Saved configuration changes are not applied",
  );
  const queuedRestart = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().endsWith("/restarts"),
  );
  await page.getByRole("button", { name: "Restart now", exact: true }).click();
  const response = await queuedRestart;
  expect(response.status()).toBe(202);
  const restarted = await response.json();
  expect(restarted.steps[0].action).toBe("restart");
  await expect
    .poll(
      async () => {
        const current = await page.request.get(
          `/api/v1/deployments/${restarted.id}`,
          { headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` } },
        );
        return (await current.json()).state;
      },
      { timeout: 60000 },
    )
    .toBe("succeeded");
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.locator(".release-banner")).toContainText("succeeded");
  await expect(live).toContainText("running:healthy");
  await page.screenshot({
    path: "../.local/ui-live-instances.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: "../.local/ui-runtime-mobile.png",
    fullPage: true,
  });
});
