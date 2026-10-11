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
  source: {
    repository: "mohsalsaleem/OpenAppPlatform",
    commit: "a".repeat(40),
    ref: "refs/heads/main",
    deliveryId: "audit-delivery",
  },
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
    if (path.endsWith("/events") || path.endsWith("/stream")) {
      await route.fulfill({
        status: 503,
        json: { message: "Live streams unavailable in this read-only fixture" },
      });
      return;
    }
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
          subject: "Audit Owner",
          kind: "session",
          status: 202,
          createdAt: "2026-10-10T12:00:00Z",
        },
        {
          id: "public-login",
          action: "auth.login",
          subject: "Anonymous",
          kind: "",
          status: 200,
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
  const sidebar = page.locator("aside");
  const applicationIcon = await sidebar
    .getByRole("link", { name: "Applications", exact: true })
    .locator("svg")
    .boundingBox();
  const targetIcon = await sidebar
    .getByRole("link", { name: "Deployment Targets", exact: true })
    .locator("svg")
    .boundingBox();
  expect(Math.abs(applicationIcon!.x - targetIcon!.x)).toBeLessThan(1);
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
  await expect(
    page.getByRole("button", { name: "Review Deployment", exact: true }),
  ).toBeFocused();
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
  await page.getByRole("tab", { name: "Audit Log", exact: true }).click();
  const auditPanel = page.locator(".panel").filter({
    has: page.getByRole("heading", { name: "Audit Log", exact: true }),
  });
  await expect(auditPanel).toContainText("Audit Owner");
  await expect(auditPanel).toContainText("Anonymous");
  await expect(auditPanel.getByText("Sign In", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Deploy Application", { exact: true }),
  ).toBeVisible();
  const deployAudit = auditPanel
    .locator(".step")
    .filter({ has: page.getByText("Deploy Application", { exact: true }) });
  await deployAudit.getByText("Request Details", { exact: true }).click();
  await expect(deployAudit.locator("code")).toContainText("a".repeat(90));
  expect(
    await page.evaluate(
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
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
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
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
        return (
          r.width > 0 && r.right > document.documentElement.clientWidth + 1
        );
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
      () =>
        document.documentElement.scrollWidth <=
        document.documentElement.clientWidth,
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

test("live release snapshots preserve busy components and submit only an available component", async ({
  page,
}) => {
  await fixtures(page);
  const grouped = {
    ...app,
    manifest: {
      ...app.manifest,
      components: [
        ...app.manifest.components,
        { ...app.manifest.components[0], name: "api", instances: 1 },
      ],
    },
  };
  const busy = {
    ...release,
    id: "live-stream-release",
    state: "attention",
    manifest: grouped.manifest,
    steps: [
      {
        component: "web",
        ordinal: 1,
        phase: "attention",
        recoveryPhase: "dispatching",
        error: "stream-only failure",
      },
    ],
  };
  await page.route(`**/api/v1/applications/${app.id}/events`, async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 200));
    await route.fulfill({
      contentType: "text/event-stream",
      body: `event: snapshot\nid: live-fixture\ndata: ${JSON.stringify({ application: grouped, deployments: [busy] })}\n\n`,
    });
  });
  const posted: unknown[] = [];
  await page.route(
    `**/api/v1/applications/${app.id}/deployments`,
    async (route) => {
      if (route.request().method() !== "POST") return route.fallback();
      const body = route.request().postDataJSON();
      posted.push(body);
      await route.fulfill({
        status: 202,
        json: {
          ...release,
          id: "selected-api-release",
          manifest: grouped.manifest,
          state: "queued",
          steps: [{ component: "api", ordinal: 1, phase: "pending" }],
        },
      });
    },
  );
  await page.goto(`/applications/${app.id}`);
  await page.getByRole("tab", { name: "Activity", exact: true }).click();
  await expect(
    page.getByText("stream-only failure", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Review Deployment", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("checkbox", { name: "web · In Use", exact: true }),
  ).toBeDisabled();
  await expect(
    dialog.getByRole("checkbox", { name: "web · In Use", exact: true }),
  ).not.toBeChecked();
  await expect(
    dialog.getByRole("checkbox", { name: "api", exact: true }),
  ).toBeChecked();
  await expect(
    dialog.getByRole("region", { name: "Deployment image changes" }),
  ).toContainText("api");
  await page.screenshot({
    path: "../.local/ui-shadcn-component-review.png",
    fullPage: true,
  });
  await dialog.getByRole("button", { name: "Deploy Now", exact: true }).click();
  await expect.poll(() => posted.length).toBe(1);
  expect(posted[0]).toEqual({
    expectedVersion: app.version,
    components: ["api"],
  });
});

test("endpoint reviews expose membership changes and uncertain publication recovery", async ({
  page,
}) => {
  await fixtures(page);
  const plan = {
    id: "route-plan",
    name: "oap-route",
    component: "web",
    url: "https://shared.example.test",
    expectedVersion: 3,
    authorityHash: "a".repeat(64),
    directoryHash: "d".repeat(64),
    members: [1, 2].map((ordinal) => ({
      ordinal,
      resourceId: `resource-${ordinal}`,
      url: `https://backend-${ordinal}.example.test`,
      retired: false,
    })),
    beforeHash: "b".repeat(64),
    desiredHash: "c".repeat(64),
    desiredConfig: "{}",
    added: ["https://backend-2.example.test"],
    removed: [],
    state: "prepared",
    createdAt: "2026-10-11T12:00:00Z",
  };
  let applied = false;
  let uncertain = false;
  let writes = 0;
  await page.route("**/api/v1/applications/audit-app/routes", (route) =>
    route.fulfill({
      json: {
        endpoints: [
          {
            name: plan.name,
            component: "web",
            url: plan.url,
            status: applied ? "in-sync" : "update-needed",
            current: applied
              ? plan.members.map((m) => m.url)
              : ["https://backend-1.example.test"],
            desired: plan.members.map((m) => m.url),
          },
        ],
        publications: uncertain
          ? [{ ...plan, state: "publishing" }]
          : applied
            ? [{ ...plan, state: "applied" }]
            : [],
      },
    }),
  );
  await page.route("**/api/v1/applications/audit-app/route-reviews", (route) =>
    route.fulfill({ status: 201, json: plan }),
  );
  await page.route(
    "**/api/v1/applications/audit-app/route-publications/route-plan",
    async (route) => {
      writes++;
      if (writes === 1) {
        uncertain = true;
        await route.fulfill({
          status: 422,
          json: { message: "Publication needs recovery" },
        });
      } else {
        applied = true;
        uncertain = false;
        await route.fulfill({ json: { ...plan, state: "applied" } });
      }
    },
  );
  await page.goto("/applications/audit-app");
  await page.getByRole("tab", { name: "Endpoints", exact: true }).click();
  await expect(page.getByRole("link", { name: plan.url })).toBeVisible();
  await page
    .getByRole("button", { name: "Review Routing", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByRole("heading", { name: "Add Backends" }),
  ).toBeVisible();
  await expect(dialog.getByText(plan.added[0], { exact: true })).toBeVisible();
  await dialog
    .getByRole("button", { name: "Apply Routing", exact: true })
    .click();
  await expect(dialog.getByText("Publication needs recovery")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(
    page.getByText("A routing update needs recovery.", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Routing", exact: true }),
  ).toBeDisabled();
  await page
    .getByRole("button", { name: "Review Recovery", exact: true })
    .click();
  await expect(
    dialog.getByRole("button", { name: "Cancel If Unchanged" }),
  ).toBeVisible();
  await dialog
    .getByRole("button", { name: "Resume Update", exact: true })
    .click();
  await expect(dialog).not.toBeVisible();
  await expect(
    page.getByText("Membership Current", { exact: false }),
  ).toBeVisible();
  await expect(page.getByText("web · Applied", { exact: true })).toBeVisible();
});
