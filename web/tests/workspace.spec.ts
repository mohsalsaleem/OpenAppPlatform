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
  const workflow = page.getByRole("region", { name: "Workflow ownership" });
  await workflow.getByText("web · OAP image releases", { exact: true }).click();
  await expect(workflow).toContainText("Supply a built image");
  await expect(workflow).toContainText(
    "OAP coordinates standard image releases",
  );
  await expect(page.getByText("api", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Deploy application" }).click();
  await expect(page.getByRole("dialog")).toContainText("can cause downtime");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page.getByRole("button", { name: "Deploy application" }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(
    page.getByRole("heading", { name: "Runtime configuration" }),
  ).toBeVisible();
  await page
    .getByLabel("Require operator-reported healthy status for web")
    .check();
  await page.getByLabel("Observation timeout for web (seconds)").fill("120");
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).toContainText("require healthy");
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).toContainText("120");
  await page.getByLabel("Container image for web").fill("nginx:1.28-alpine");
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).toContainText("nginx:1.28-alpine");
  await page
    .getByLabel("Environment variables for web")
    .fill('{"PRIVATE_MARKER":"hidden-from-diff"}');
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).toContainText("PRIVATE_MARKER");
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).not.toContainText("hidden-from-diff");
  await page.screenshot({
    path: "../.local/ui-configuration-diff.png",
    fullPage: true,
  });
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
  await page
    .getByText("Set up an existing Coolify connection", { exact: true })
    .click();
  await page
    .getByLabel("Coolify URL", { exact: true })
    .fill("https://coolify.example.test");
  await page.getByLabel("Coolify project ID").fill("example-project");
  await page.getByLabel("Coolify server ID").fill("example-server");
  await expect(
    page.getByRole("button", { name: "Copy target configuration" }),
  ).toBeVisible();
  await expect(
    page
      .locator("details")
      .filter({ hasText: "Set up an existing Coolify connection" })
      .locator("pre"),
  ).toContainText('"tokenEnv": "COOLIFY_TOKEN"');
  await page.screenshot({
    path: "../.local/ui-target-setup.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Verify connection", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: "Connection verified" }),
  ).toBeVisible();
  await page.getByText("OAP adapter capabilities", { exact: true }).click();
  const adapterCapabilities = page
    .locator("details")
    .filter({ hasText: "OAP adapter capabilities" });
  await expect(adapterCapabilities).toContainText("Blue-green deployment");
  await expect(adapterCapabilities).toContainText("Unavailable in OAP");
  await page.screenshot({
    path: "../.local/ui-adapter-capabilities.png",
    fullPage: true,
  });
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
  await page.getByRole("tab", { name: "Settings" }).click();
  await page
    .getByLabel("Container image for web")
    .fill(process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!);
  await page.getByLabel("Internal port for web").fill("8025");
  await page.getByLabel("Health check mode for web").selectOption("http");
  await page.getByLabel("Health check path for web").fill("/health/custom");
  await page.getByLabel("Interval for web (seconds)").fill("1");
  await page.getByLabel("Attempt timeout for web (seconds)").fill("1");
  await page.getByLabel("Retries for web", { exact: true }).fill("1");
  await page.getByLabel("Start period for web (seconds)").fill("0");
  await expect(
    page.getByRole("region", { name: "Configuration changes" }),
  ).toContainText("HTTP GET /health/custom");
  await page
    .getByLabel("Health check path for web")
    .fill("https://outside.invalid/health");
  await expect(
    page.getByRole("button", { name: "Save configuration" }),
  ).toBeDisabled();
  await page.getByLabel("Health check path for web").fill("/health/custom");
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
  await expect(
    page.getByText("HTTP GET /health/custom", { exact: false }).first(),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Activity" }).click();
  await page
    .getByRole("button", { name: "Compare selected releases", exact: true })
    .click();
  const comparison = page.getByRole("region", { name: "Release comparison" });
  await expect(comparison).toContainText("internal port");
  await expect(comparison).toContainText("8025");
  await expect(comparison).toContainText("Changed (value hidden)");
  await expect(comparison).not.toContainText("browser-v2");
  const selectedFrom = await page
    .getByLabel("From release", { exact: true })
    .inputValue();
  const selectedTo = await page
    .getByLabel("To release", { exact: true })
    .inputValue();
  const historyPattern = `**/api/v1/applications/${page.url().split("/").at(-1)}/deployments`;
  await page.route(historyPattern, async (route) => {
    const response = await route.fetch();
    const history = await response.json();
    await route.fulfill({
      response,
      json: [
        { ...history[0], id: "11111111111111111111111111111111" },
        ...history,
      ],
    });
  });
  await expect(
    page.getByLabel("From release", { exact: true }).locator("option").first(),
  ).toHaveAttribute("value", "11111111111111111111111111111111");
  await expect(page.getByLabel("From release", { exact: true })).toHaveValue(
    selectedFrom,
  );
  await expect(page.getByLabel("To release", { exact: true })).toHaveValue(
    selectedTo,
  );
  await page.unroute(historyPattern);
  await page
    .getByRole("button", {
      name: `Review rollback to ${selectedFrom.slice(0, 8)}`,
      exact: true,
    })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "snapshot configuration/topology differs",
  );
  await expect(
    page.getByRole("button", { name: "Roll back images", exact: true }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();

  await comparison.screenshot({ path: "../.local/ui-release-comparison.png" });
  await page.getByRole("tab", { name: "Overview" }).click();
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

  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByLabel("Instances for web").fill("2");
  await page.getByRole("button", { name: "Save configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  await page
    .getByRole("button", { name: "Scale down web", exact: true })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "Containers and volumes are retained",
  );
  await expect(page.getByRole("dialog")).toContainText("web / 2");
  const queuedRetirement = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().endsWith("/scale-down"),
  );
  await page
    .getByRole("button", { name: "Retire instances", exact: true })
    .click();
  const retirementResponse = await queuedRetirement;
  expect(retirementResponse.status()).toBe(202);
  const retirement = await retirementResponse.json();
  expect(retirement.operation).toBe("scale-down");
  await expect
    .poll(
      async () => {
        const response = await page.request.get(
          `/api/v1/deployments/${retirement.id}`,
          { headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` } },
        );
        return (await response.json()).state;
      },
      { timeout: 60000 },
    )
    .toBe("succeeded");
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(page.getByText("1 configured", { exact: true })).toBeVisible();
  await expect(live).toContainText("retained after retirement");
  await expect(
    page.getByRole("button", { name: "Restart web / 2", exact: true }),
  ).toBeDisabled();
  await expect(page.locator(".release-banner")).toContainText("succeeded");
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

test("group existing Docker services without deploying or restarting", async ({
  page,
}) => {
  test.skip(
    !process.env.OAP_TEST_OBSERVED_RESOURCE,
    "Requires isolated observation fixture",
  );
  await page.goto("/");
  await page
    .getByLabel("Platform access token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to workspace" }).click();
  await page.getByRole("link", { name: "Deployment targets" }).click();
  await page.getByRole("link", { name: "Group services" }).click();
  const apps = await (
    await page.request.get("/api/v1/applications", {
      headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` },
    })
  ).json();
  const source = apps.find((a: { manifest: { name: string } }) =>
    /^browser-[0-9]/.test(a.manifest.name),
  );
  await page.getByLabel("Reuse a component layout").selectOption(source.id);
  await page.getByLabel("Select existing-observation-fixture").check();
  await page.getByRole("button", { name: "Review 1 services" }).click();
  await page
    .getByLabel("Application name", { exact: true })
    .fill(`observed-browser-${Date.now()}`);
  await page
    .getByLabel("Component name for existing-observation-fixture")
    .fill("web");
  await page.screenshot({
    path: "../.local/ui-assembly-review.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Create observed application" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Observing existing services" }),
  ).toBeVisible();
  const observedWorkflow = page.getByRole("region", {
    name: "Workflow ownership",
  });
  await observedWorkflow
    .getByText("web · Existing workflow", { exact: true })
    .click();
  await expect(observedWorkflow).toContainText("OAP does not start builds");
  await expect(observedWorkflow).toContainText(
    "OAP restart is blocked while observing",
  );
  await expect(
    page.getByRole("button", { name: "Observe-only application" }),
  ).toBeDisabled();
  await expect(
    page.getByRole("button", { name: "Restart web / 1", exact: true }),
  ).toBeDisabled();
  await expect(
    page.locator(".step").filter({ hasText: "web / 1" }),
  ).toContainText("running");
  await page.getByRole("button", { name: "View web instance 1 logs" }).click();
  await expect(page.locator(".log-output")).not.toContainText(
    "Select a deployed component",
  );
  await page.getByRole("tab", { name: "Activity" }).click();
  await expect(page.getByText("No deployments yet.")).toBeVisible();
  await page.getByRole("tab", { name: "Overview" }).click();
  await page.screenshot({
    path: "../.local/ui-observed-application.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "Deployment targets" }).click();
  await page.getByRole("link", { name: "Group services" }).click();
  await expect(
    page.getByLabel("Select existing-observation-fixture"),
  ).toBeDisabled();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.screenshot({
    path: "../.local/ui-assembly-mobile.png",
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});

test("release control review explains retained changes and abandonment fences", async ({
  page,
}) => {
  await page.goto("/");
  await page
    .getByLabel("Platform access token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Your applications" }),
  ).toBeVisible();
  const headers = { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` };
  const apps = await (
    await page.request.get("/api/v1/applications", { headers })
  ).json();
  const app = apps.find((a: { manifest: { name: string } }) =>
    /^browser-[0-9]/.test(a.manifest.name),
  );
  expect(app).toBeTruthy();
  let release = {
    id: "f".repeat(32),
    applicationId: app.id,
    definitionVersion: app.version,
    state: "queued",
    operation: "deploy",
    manifest: app.manifest,
    createdAt: new Date().toISOString(),
    updatedAt: new Date().toISOString(),
    steps: [{ component: "web", ordinal: 1, phase: "pending" }],
    control: undefined as
      undefined | { mode: string; reason: string; at: string },
  };
  await page.route(`**/api/v1/applications/${app.id}/deployments`, (route) =>
    route.fulfill({ json: [release] }),
  );
  await page.route(
    `**/api/v1/deployments/${release.id}/control`,
    async (route) => {
      const body = route.request().postDataJSON();
      expect(body.mode).toBe("cancel");
      expect(body.acknowledgePreparedChanges).toBeTruthy();
      release = {
        ...release,
        state: "cancelled",
        control: {
          mode: "cancel",
          reason: body.reason,
          at: new Date().toISOString(),
        },
      };
      await route.fulfill({ json: release });
    },
  );
  await page.goto(`/applications/${app.id}`);
  await page.getByRole("tab", { name: "Activity" }).click();
  await page.getByRole("button", { name: "Review cancellation" }).click();
  const dialog = page.getByRole("dialog", { name: "Cancel before dispatch" });
  await expect(dialog).toContainText(
    "Prepared resources and configuration may remain",
  );
  await expect(
    dialog.getByRole("button", { name: "Confirm cancellation" }),
  ).toBeDisabled();
  await dialog.getByLabel("Reason").fill("Defer this release");
  await dialog.getByRole("checkbox").check();
  await expect(
    dialog.getByRole("button", { name: "Confirm cancellation" }),
  ).toBeEnabled();
  await page.screenshot({
    path: "../.local/ui-release-cancellation.png",
    fullPage: true,
  });
  await dialog.getByRole("button", { name: "Confirm cancellation" }).click();
  await expect(dialog).not.toBeVisible();
  await expect(
    page.getByText("Cancelled before dispatch. Prepared changes may remain.", {
      exact: false,
    }),
  ).toBeVisible();
  release = {
    ...release,
    state: "abandoned",
    control: {
      mode: "abandon",
      reason: "Investigate uncertain operation",
      at: new Date().toISOString(),
    },
  };
  await page.reload();
  await expect(
    page.getByRole("button", { name: "Reconciliation required" }),
  ).toBeDisabled();
  await page.getByRole("tab", { name: "Activity" }).click();
  await expect(
    page.getByText("Application fenced until owner reconciliation.", {
      exact: false,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Review reconciliation" }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "Every possible provider operation must be terminal",
  );
});
