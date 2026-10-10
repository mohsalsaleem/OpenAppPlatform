import { test, expect } from "@playwright/test";

test("authentication, application setup, release confirmation, and target discovery", async ({
  page,
}) => {
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto("/");
  await page.getByLabel("Platform Access Token").fill("invalid");
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "valid platform access token",
  );
  await page
    .getByLabel("Platform Access Token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
  ).toBeVisible();
  await page.getByRole("link", { name: "New Application" }).click();
  const name = `browser-${Date.now()}`;
  await page.getByLabel("Application Name", { exact: true }).fill(name);
  await page.getByRole("button", { name: "Add Component" }).click();
  await page.getByLabel("Component Name", { exact: true }).nth(1).fill("api");
  await page
    .locator("details.advanced-configuration")
    .filter({ has: page.getByLabel("Environment Variables for web") })
    .locator("summary")
    .click();
  await page.getByLabel("Environment Variables for web").fill('{"unfinished":');
  await expect(
    page.getByRole("button", { name: "Create Application" }),
  ).toBeDisabled();
  await page.getByRole("button", { name: "Remove Component 1" }).click();
  await expect(
    page.getByRole("button", { name: "Create Application" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Add Component" }).click();
  await page.getByLabel("Component Name", { exact: true }).nth(1).fill("web");
  await page.getByRole("button", { name: "Create Application" }).click();
  await expect(page.getByRole("heading", { name })).toBeVisible();
  await expect(page.getByText("Ready for Your First Deployment")).toBeVisible();
  await page.getByRole("tab", { name: "Settings" }).click();
  const workflow = page.getByRole("region", { name: "Workflow Ownership" });
  await workflow.getByText("web · OAP Image Releases", { exact: true }).click();
  await expect(workflow).toContainText("Supply a built image");
  await expect(workflow).toContainText(
    "Open App Platform coordinates image deployments",
  );
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(
    page
      .getByRole("tabpanel", { name: "Overview" })
      .getByText("api", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Review Deployment" }).click();
  await expect(page.getByRole("dialog")).toContainText("can cause downtime");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).not.toBeVisible();
  await page.getByRole("button", { name: "Review Deployment" }).click();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(
    page.getByRole("heading", { name: "Runtime Configuration" }),
  ).toBeVisible();
  await page.getByLabel("Require Healthy Status for web").check();
  await page.getByLabel("Observation Timeout for web (seconds)").fill("120");
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).toContainText("require healthy");
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).toContainText("120");
  await page.getByLabel("Container Image for web").fill("nginx:1.28-alpine");
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).toContainText("nginx:1.28-alpine");
  await page
    .locator("details.advanced-configuration")
    .filter({ has: page.getByLabel("Environment Variables for web") })
    .locator("summary")
    .click();
  await page
    .getByLabel("Environment Variables for web")
    .fill('{"PRIVATE_MARKER":"hidden-from-diff"}');
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).toContainText("PRIVATE_MARKER");
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).not.toContainText("hidden-from-diff");
  await page.screenshot({
    path: "../.local/ui-configuration-diff.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Save Configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await page.getByText("View Application Definition").click();
  await expect(
    page.getByRole("tabpanel", { name: "Settings" }).locator("pre"),
  ).toContainText("nginx:1.28-alpine");
  await page.screenshot({
    path: "../.local/ui-configuration.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "Deployment Targets" }).click();
  await page
    .getByText("Set Up an Existing Coolify Connection", { exact: true })
    .click();
  await page
    .getByLabel("Coolify URL", { exact: true })
    .fill("https://coolify.example.test");
  await page.getByLabel("Coolify Project ID").fill("example-project");
  await page.getByLabel("Coolify Server ID").fill("example-server");
  await expect(
    page.getByRole("button", { name: "Copy Target Configuration" }),
  ).toBeVisible();
  await expect(
    page
      .locator("details")
      .filter({ hasText: "Set Up an Existing Coolify Connection" })
      .locator("pre"),
  ).toContainText('"tokenEnv": "COOLIFY_TOKEN"');
  await page.screenshot({
    path: "../.local/ui-target-setup.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Verify Connection", exact: true })
    .click();
  await expect(
    page.getByRole("status").filter({ hasText: "Connection verified" }),
  ).toBeVisible();
  await page.getByText("Adapter Capabilities", { exact: true }).click();
  const adapterCapabilities = page
    .locator("details")
    .filter({ hasText: "Adapter Capabilities" });
  await expect(adapterCapabilities).toContainText("Blue-green deployment");
  await expect(adapterCapabilities).toContainText("Unavailable in OAP");
  await page.screenshot({
    path: "../.local/ui-adapter-capabilities.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Inspect Resources" }).click();
  await expect(
    page.getByText(/resources in this target environment/),
  ).toBeVisible();
  await page.getByRole("link", { name: "Applications", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
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
    await page.getByRole("button", { name: "Review Deployment" }).click();
    const queued = page.waitForResponse(
      (response) =>
        response.request().method() === "POST" &&
        response.url().endsWith("/deployments"),
    );
    await page.getByRole("button", { name: "Deploy Now" }).click();
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
      page.getByRole("button", { name: "Review Deployment" }),
    ).toBeEnabled();
  }
  await page.goto("/");
  await page
    .getByLabel("Platform Access Token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await page.getByRole("link", { name: "New Application" }).click();
  await page
    .getByLabel("Application Name", { exact: true })
    .fill(`browser-live-${Date.now()}`);
  await page
    .getByLabel("Container Image", { exact: true })
    .fill(process.env.OAP_TEST_DOCKER_IMAGE!);
  await page.getByLabel("Internal Port", { exact: true }).fill("8080");
  await page
    .locator("details.advanced-configuration")
    .filter({ has: page.getByLabel("Environment Variables for web") })
    .locator("summary")
    .click();
  await page
    .getByLabel("Environment Variables for web")
    .fill('{"OAP_TEST_MESSAGE":');
  await expect(
    page.getByRole("button", { name: "Create Application" }),
  ).toBeDisabled();
  await page
    .getByLabel("Environment Variables for web")
    .fill('{"OAP_TEST_MESSAGE":"browser-v1"}');
  await page
    .getByLabel("Service Connections for web")
    .fill('{"UPSTREAM_URL":"web"}');
  await page.getByRole("button", { name: "Create Application" }).click();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  const live = page
    .locator("section")
    .filter({ has: page.getByRole("heading", { name: "Live Instances" }) });
  await expect(live).toContainText("Healthy", { timeout: 15000 });
  await page.getByRole("button", { name: "View web instance 1 logs" }).click();
  await expect(page.locator(".log-output")).toContainText("fixture", {
    timeout: 5000,
  });
  await page.getByRole("tab", { name: "Settings" }).click();
  await page
    .getByLabel("Container Image for web")
    .fill(process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!);
  await page.getByLabel("Internal Port for web").fill("8025");
  await page.getByLabel("Health Check Mode for web").selectOption("http");
  await page.getByLabel("Health Check Path for web").fill("/health/custom");
  await page.getByText("Check Timing", { exact: true }).click();
  await page.getByLabel("Interval for web (seconds)").fill("1");
  await page.getByLabel("Attempt Timeout for web (seconds)").fill("1");
  await page.getByLabel("Retries for web", { exact: true }).fill("1");
  await page.getByLabel("Start Period for web (seconds)").fill("0");
  await expect(
    page.getByRole("region", { name: "Configuration Changes" }),
  ).toContainText("HTTP GET /health/custom");
  await page
    .getByLabel("Health Check Path for web")
    .fill("https://outside.invalid/health");
  await expect(
    page.getByRole("button", { name: "Save Configuration" }),
  ).toBeDisabled();
  await page.getByLabel("Health Check Path for web").fill("/health/custom");
  await page
    .locator("details.advanced-configuration")
    .filter({ has: page.getByLabel("Environment Variables for web") })
    .locator("summary")
    .click();
  await page
    .getByLabel("Environment Variables for web")
    .fill('{"OAP_TEST_MESSAGE":"browser-v2"}');
  await page.getByRole("button", { name: "Save Configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(live).toContainText("Healthy", { timeout: 15000 });
  await expect(
    page
      .getByRole("tabpanel", { name: "Overview" })
      .locator(".image-ref")
      .first(),
  ).toContainText(process.env.OAP_TEST_DOCKER_REPLACEMENT_IMAGE!);
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
  await expect(page.locator(".release-banner")).toContainText("Completed");
  await expect(
    page.getByText("HTTP GET /health/custom", { exact: false }).first(),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Activity" }).click();
  await page
    .getByRole("button", { name: "Compare Selected Releases", exact: true })
    .click();
  const comparison = page.getByRole("region", { name: "Release Comparison" });
  await expect(comparison).toContainText("internal port");
  await expect(comparison).toContainText("8025");
  await expect(comparison).toContainText("Changed (value hidden)");
  await expect(comparison).not.toContainText("browser-v2");
  const selectedFrom = await page
    .getByLabel("From Release", { exact: true })
    .inputValue();
  const selectedTo = await page
    .getByLabel("To Release", { exact: true })
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
    page.getByLabel("From Release", { exact: true }).locator("option").first(),
  ).toHaveAttribute("value", "11111111111111111111111111111111");
  await expect(page.getByLabel("From Release", { exact: true })).toHaveValue(
    selectedFrom,
  );
  await expect(page.getByLabel("To Release", { exact: true })).toHaveValue(
    selectedTo,
  );
  await page.unroute(historyPattern);
  await page
    .getByRole("button", {
      name: `Review Rollback to ${selectedFrom.slice(0, 8)}`,
      exact: true,
    })
    .click();
  await expect(page.getByRole("dialog")).toContainText(
    "snapshot configuration/topology differs",
  );
  await expect(
    page.getByRole("button", { name: "Roll Back Images", exact: true }),
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
  await page.getByRole("button", { name: "Restart Now", exact: true }).click();
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
  await expect(page.locator(".release-banner")).toContainText("Completed");
  await expect(live).toContainText("Healthy");

  await page.getByRole("tab", { name: "Settings" }).click();
  await page.getByLabel("Instances for web").fill("2");
  await page.getByRole("button", { name: "Save Configuration" }).click();
  await expect(
    page.getByText("Configuration saved. Deploy when you are ready."),
  ).toBeVisible();
  await deployAndWait();
  await page.getByRole("tab", { name: "Overview" }).click();
  await page
    .getByRole("button", { name: "Scale Down web", exact: true })
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
    .getByRole("button", { name: "Retire Instances", exact: true })
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
  await expect(page.locator(".release-banner")).toContainText("Completed");
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
    .getByLabel("Platform Access Token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await page.getByRole("link", { name: "Deployment Targets" }).click();
  await page.getByRole("link", { name: "Group Services" }).click();
  const apps = await (
    await page.request.get("/api/v1/applications", {
      headers: { Authorization: `Bearer ${process.env.OAP_API_TOKEN}` },
    })
  ).json();
  const source = apps.find((a: { manifest: { name: string } }) =>
    /^browser-[0-9]/.test(a.manifest.name),
  );
  await page.getByLabel("Reuse a Component Layout").selectOption(source.id);
  await page.getByLabel("Select existing-observation-fixture").check();
  await page.getByRole("button", { name: "Review Selection (1)" }).click();
  await page
    .getByLabel("Application Name", { exact: true })
    .fill(`observed-browser-${Date.now()}`);
  await page
    .getByLabel("Component Name for existing-observation-fixture")
    .fill("web");
  await page.screenshot({
    path: "../.local/ui-assembly-review.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Create Observed Application" })
    .click();
  await expect(
    page.getByRole("heading", { name: "Observing Existing Services" }),
  ).toBeVisible();
  await page.getByRole("tab", { name: "Settings" }).click();
  const observedWorkflow = page.getByRole("region", {
    name: "Workflow Ownership",
  });
  await observedWorkflow
    .getByText("web · Existing Workflow", { exact: true })
    .click();
  await expect(observedWorkflow).toContainText("OAP does not start builds");
  await expect(observedWorkflow).toContainText(
    "OAP restart is blocked while observing",
  );
  await expect(
    page.getByText("Managed Externally", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Deployment" }),
  ).toHaveCount(0);
  await page.getByRole("tab", { name: "Overview" }).click();
  await expect(
    page.getByRole("button", { name: "Restart web / 1", exact: true }),
  ).toBeDisabled();
  await expect(
    page.locator(".step").filter({ hasText: "web / 1" }),
  ).toContainText("Healthy");
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
  await page.getByRole("link", { name: "Deployment Targets" }).click();
  await page.getByRole("link", { name: "Group Services" }).click();
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
    .getByLabel("Platform Access Token")
    .fill(process.env.OAP_API_TOKEN!);
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
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
  await page.getByRole("button", { name: "Review Cancellation" }).click();
  const dialog = page.getByRole("dialog", { name: "Cancel Before Dispatch" });
  await expect(dialog).toContainText(
    "Prepared resources and configuration may remain",
  );
  await expect(
    dialog.getByRole("button", { name: "Confirm Cancellation" }),
  ).toBeDisabled();
  await dialog.getByLabel("Reason").fill("Defer this release");
  await dialog.getByRole("checkbox").check();
  await expect(
    dialog.getByRole("button", { name: "Confirm Cancellation" }),
  ).toBeEnabled();
  await page.screenshot({
    path: "../.local/ui-release-cancellation.png",
    fullPage: true,
  });
  await dialog.getByRole("button", { name: "Confirm Cancellation" }).click();
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
    page.getByRole("button", { name: "Reconciliation Required" }),
  ).toBeDisabled();
  await page.getByRole("tab", { name: "Activity" }).click();
  await expect(
    page.getByText("Application fenced until owner reconciliation.", {
      exact: false,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Review Reconciliation" }).click();
  await expect(page.getByRole("dialog")).toContainText(
    "Every possible provider operation must be terminal",
  );
});
