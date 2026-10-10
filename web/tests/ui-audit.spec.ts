import { test, expect, type Page } from "@playwright/test";

const image = "example.test/team/web@sha256:" + "a".repeat(64);
const proposed = "example.test/team/web@sha256:" + "b".repeat(64);
const app = {
  id: "audit-app",
  groupId: "audit-group",
  version: 3,
  createdAt: "2026-10-10T12:00:00Z",
  updatedAt: "2026-10-10T12:00:00Z",
  manifest: {
    name: "audit-app",
    environment: "staging",
    targetId: "audit-target",
    components: [
      {
        name: "web",
        kind: "web",
        image: proposed,
        port: 8080,
        instances: 2,
        strategy: "standard",
      },
    ],
  },
};
const target = {
  id: "audit-target",
  name: "Staging Coolify",
  operator: "coolify",
  environment: "staging",
  url: "https://coolify.example.test",
};
const release = {
  id: "audit-release",
  applicationId: app.id,
  definitionVersion: 3,
  state: "succeeded",
  operation: "deploy",
  manifest: {
    ...app.manifest,
    components: [{ ...app.manifest.components[0], image }],
  },
  steps: [
    {
      component: "web",
      ordinal: 1,
      phase: "succeeded",
      rollbackFrom: "older-release",
    },
  ],
  createdAt: "2026-10-10T12:00:00Z",
  updatedAt: "2026-10-10T12:00:00Z",
};
async function fixtures(page: Page, preview = false) {
  await page.route("**/api/v1/**", async (route) => {
    const path = new URL(route.request().url()).pathname.replace("/api/v1", "");
    const routes: Record<string, unknown> = {
      "/auth/status": {
        mode: preview ? "preview" : "owner",
        setupRequired: false,
      },
      "/auth/me": preview
        ? null
        : { name: "Audit Owner", role: "owner", kind: "session" },
      "/meta": {},
      "/targets": [target],
      "/application-groups": [
        {
          id: "audit-group",
          name: "audit-app",
          version: 1,
          environments: [app],
        },
      ],
      "/applications": [app],
      [`/applications/${app.id}`]: app,
      [`/applications/${app.id}/deployments`]: [
        release,
        { ...release, id: "older-release", steps: [] },
      ],
      [`/applications/${app.id}/instances`]: [1, 2].map((ordinal) => ({
        component: "web",
        ordinal,
        resourceId: `resource-${ordinal}`,
        status: "running:healthy",
        retired: false,
        checkedAt: "2026-10-10T12:00:00Z",
        resource: {
          id: `resource-${ordinal}`,
          name: "web",
          image,
          status: "running:healthy",
          url: "https://web.example.test",
        },
      })),
      [`/applications/${app.id}/logs/web`]: {
        logs: "fixture ready\nrequest completed\n",
      },
      [`/applications/${app.id}/source-events`]: [
        {
          id: "event",
          state: "released",
          deploymentId: release.id,
          commit: "a".repeat(40),
          definitionVersion: 3,
          binding: { repository: "team/web", branch: "main" },
          builds: { web: { id: "build", state: "built", image } },
          updatedAt: "2026-10-10T12:00:00Z",
        },
      ],
      "/targets/audit-target/capabilities": {
        standard: true,
        restart: true,
        httpHealthChecks: true,
        environment: true,
        serviceEndpoints: true,
        imageRollback: false,
      },
      "/targets/audit-target/resources": [
        {
          id: "selectable",
          name: "Existing Web",
          status: "running:healthy",
          artifactKind: "image",
          image,
          port: 8080,
        },
        {
          id: "grouped",
          name: "Already Grouped Web",
          status: "running:healthy",
          artifactKind: "image",
          applicationId: app.id,
        },
      ],
      "/access/members": [
        {
          id: "member",
          name: "Audit Owner",
          email: "owner@example.test",
          role: "owner",
          active: true,
        },
      ],
      "/access/credentials": [],
      "/access/audit": [
        {
          id: "event",
          action:
            "POST /api/v1/applications/" + "a".repeat(90) + "/deployments",
          subject: "member",
          kind: "session",
          status: 202,
          createdAt: "2026-10-10T12:00:00Z",
        },
      ],
    };
    if (route.request().method() !== "GET")
      throw new Error("Audit fixture must not mutate workloads");
    if (!(path in routes)) throw new Error(`Missing audit fixture: ${path}`);
    await route.fulfill({ json: routes[path] });
  });
}

test("operational hierarchy, deployment image differences, source result, keyboard tabs and retained drafts", async ({
  page,
}) => {
  await fixtures(page);
  await page.goto(`/applications/${app.id}`);
  await expect(page.locator(".runtime-summary")).toHaveText(
    "2/2 healthy · 2/2 running",
  );
  await expect(
    page.getByText("Saved image differs from the running image.", {
      exact: false,
    }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Workflow Ownership" }),
  ).not.toBeVisible();
  const components = await page
    .getByRole("heading", { name: "Components", exact: true })
    .boundingBox();
  const latest = await page.locator(".release-banner").boundingBox();
  expect(components!.y).toBeLessThan(latest!.y);
  await page
    .getByRole("button", { name: "Review Deployment", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("region", { name: "Deployment image changes" }),
  ).toContainText(image);
  await expect(dialog).toContainText(proposed);
  await expect(dialog).toContainText("1 component on Staging Coolify");
  await page.keyboard.press("Escape");
  const overview = page.getByRole("tab", { name: "Overview" });
  await overview.focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Activity" })).toBeFocused();
  await expect(page.getByRole("tab", { name: "Activity" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.getByRole("tabpanel", { name: "Activity" })).toBeVisible();
  await expect(
    page.getByRole("tabpanel", { name: "Activity" }),
  ).not.toContainText("release queued");
  await expect(
    page.getByLabel("From Release").locator("option").first(),
  ).toContainText("Rollback");
  await expect(page.locator(".recovery-panel")).toHaveCount(0);
  await expect(page.getByRole("tabpanel", { name: "Activity" })).toContainText(
    "Built",
  );
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(
    page.getByRole("heading", { name: "Environment Organization" }),
  ).toHaveCount(0);
  await page.getByLabel("Health Check Mode for web").selectOption("http");
  await expect(page.getByLabel("Require Healthy Status for web")).toBeChecked();
  await expect(
    page.getByLabel("Require Healthy Status for web"),
  ).toBeDisabled();
  await page
    .getByLabel("Health Check Path for web")
    .fill("https://outside.example/health");
  await expect(page.getByLabel("Health Check Path for web")).toHaveAttribute(
    "aria-invalid",
    "true",
  );
  await expect(
    page.getByRole("alert").filter({ hasText: "Use a path starting with /" }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Save Configuration" }),
  ).toBeDisabled();
  await page.getByRole("tab", { name: "Logs" }).click();
  await page.getByRole("button", { name: "web / 1", exact: true }).click();
  await expect(page.locator(".log-output")).toHaveText(
    "fixture ready\nrequest completed\n",
  );
  await page.getByRole("tab", { name: "Settings" }).click();
  await expect(page.getByLabel("Health Check Mode for web")).toHaveValue(
    "http",
  );
  await expect(page.getByLabel("Health Check Path for web")).toHaveValue(
    "https://outside.example/health",
  );
  await page.getByLabel("Health Check Path for web").fill("/ready");
  await expect(
    page.getByRole("button", { name: "Save Configuration" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Reload Configuration" }).click();
  await expect(page.getByLabel("Health Check Mode for web")).toHaveValue(
    "existing",
  );
  await page.getByRole("tab", { name: "Overview" }).click();
  await page.screenshot({
    path: "../.local/ui-audit-fixed-overview.png",
    fullPage: true,
  });
});

test("workspace settings, readable mobile navigation and long audit details stay within viewport", async ({
  page,
}) => {
  await fixtures(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto("/");
  await expect(
    page.getByRole("navigation", { name: "Infrastructure" }),
  ).toContainText("Deployment Targets");
  await expect(page.locator(".environment-summary")).toContainText(
    "2/2 healthy",
  );
  await page.getByRole("link", { name: "Workspace Settings" }).click();
  await expect(
    page.getByRole("heading", { name: "Workspace Settings" }),
  ).toBeVisible();
  await expect(page.locator(".topbar")).toContainText("Workspace Access");
  await page.getByRole("button", { name: "Audit Log", exact: true }).click();
  await expect(
    page.getByText("Deploy Application", { exact: true }),
  ).toBeVisible();
  await page.getByText("Request Details", { exact: true }).click();
  await expect(page.locator("code")).toContainText("a".repeat(90));
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: "../.local/ui-audit-fixed-access-mobile.png",
    fullPage: true,
  });
  await page
    .getByRole("link", { name: "Deployment Targets", exact: true })
    .click();
  await page.getByRole("link", { name: "Group Services" }).click();
  await page.getByLabel("Search Services").fill("Existing");
  await expect(page.getByLabel("Select Already Grouped Web")).toHaveCount(0);
  await page.getByLabel("Select Existing Web").check();
  await expect(
    page.getByRole("button", { name: "Review Selection (1)" }),
  ).toBeEnabled();
  await page.getByLabel("Search Services").fill("");
  await page
    .getByRole("combobox", { name: "Availability", exact: true })
    .selectOption("grouped");
  await expect(page.getByLabel("Select Already Grouped Web")).toBeDisabled();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: "../.local/ui-audit-fixed-grouping-mobile.png",
    fullPage: true,
  });
});

test("preview navigation, instance rows and long images fit on mobile", async ({
  page,
}) => {
  await fixtures(page, true);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/applications/${app.id}`);
  await page.getByLabel("Platform Access Token").fill("ui-fixture-token");
  await page.getByRole("button", { name: "Connect to Workspace" }).click();
  await expect(
    page.getByRole("heading", { name: "audit-app", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Disconnect", exact: true }),
  ).toBeVisible();
  const overflow = await page.evaluate(() =>
    [...document.querySelectorAll("body *")]
      .filter((el) => {
        const r = el.getBoundingClientRect();
        return r.width > 0 && r.right > innerWidth + 1;
      })
      .slice(0, 8)
      .map((el) => ({
        tag: el.tagName,
        class: el.className,
        right: el.getBoundingClientRect().right,
      })),
  );
  expect(overflow).toEqual([]);
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
  await page.screenshot({
    path: "../.local/ui-audit-fixed-runtime-mobile.png",
    fullPage: true,
  });
});

test("delayed log responses preserve navigation and the latest selected instance", async ({
  page,
}) => {
  await fixtures(page);
  let completeFirst: (() => void) | undefined;
  const first = new Promise<void>((resolve) => {
    completeFirst = resolve;
  });
  let requestStarted: (() => void) | undefined;
  const started = new Promise<void>((resolve) => {
    requestStarted = resolve;
  });
  await page.route(
    `**/api/v1/applications/${app.id}/logs/web?ordinal=1`,
    async (route) => {
      requestStarted!();
      await first;
      await route.fulfill({ json: { logs: "older instance response" } });
    },
  );
  await page.route(
    `**/api/v1/applications/${app.id}/logs/web?ordinal=2`,
    (route) => route.fulfill({ json: { logs: "latest instance response" } }),
  );
  await page.goto(`/applications/${app.id}`);
  await page.getByRole("tab", { name: "Logs" }).click();
  await page.getByRole("button", { name: "web / 1", exact: true }).click();
  await started;
  await expect(page.locator(".log-output")).toHaveText("Fetching logs…");
  await page.getByRole("button", { name: "web / 2", exact: true }).click();
  await expect(page.locator(".log-output")).toHaveText(
    "latest instance response",
  );
  await page.getByRole("tab", { name: "Settings" }).click();
  const finished = page.waitForResponse((response) =>
    response.url().endsWith("/logs/web?ordinal=1"),
  );
  completeFirst!();
  await finished;
  await expect(page.getByRole("tab", { name: "Settings" })).toHaveAttribute(
    "aria-selected",
    "true",
  );
  await expect(page.locator(".log-output")).toHaveText(
    "latest instance response",
  );
});
