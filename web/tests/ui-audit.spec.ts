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
 "/targets/audit-target/connection-state": {state:"unknown",observedState:"unknown",message:"Connection has not been checked for this configuration.",nativeSourceObservation:false,heldOperations:[]},
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
  await page.getByLabel("Health Check Mode for web").click();
  await page
    .getByRole("option", { name: "Container HTTP Check", exact: true })
    .click();
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
  await expect(page.getByLabel("Health Check Mode for web")).toHaveText(
    "Container HTTP Check",
  );
  await expect(page.getByLabel("Health Check Path for web")).toHaveValue(
    "https://outside.example/health",
  );
  await page.getByLabel("Health Check Path for web").fill("/ready");
  await expect(
    page.getByRole("button", { name: "Save Configuration" }),
  ).toBeEnabled();
  await page.getByRole("button", { name: "Reload Configuration" }).click();
  await expect(page.getByLabel("Health Check Mode for web")).toHaveText(
    "Keep Existing Health Check",
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

test("endpoint readiness shows DNS TLS expiry and failures separately from membership", async ({
  page,
}) => {
  await fixtures(page);
  let report: unknown = undefined;
  let calls = 0;
  await page.route("**/api/v1/applications/audit-app/routes", (route) =>
    route.fulfill({
      json: {
        endpoints: [
          {
            name: "oap-route",
            component: "web",
            url: "https://shared.example.test",
            status: "in-sync",
            current: ["https://one.example.test"],
            desired: ["https://one.example.test"],
            verification: report,
          },
        ],
        publications: [],
      },
    }),
  );
  await page.route(
    "**/api/v1/applications/audit-app/endpoint-verifications",
    async (route) => {
      calls++;
      report = {
        status: calls === 1 ? "verified" : "failed",
        checkedAt: "2026-10-11T12:00:00Z",
        dns: calls === 1 ? "matched" : "mismatch",
        addresses: ["192.0.2.1"],
        tls: calls === 1 ? "valid" : "skipped",
        health: calls === 1 ? "ready" : "skipped",
        httpStatus: calls === 1 ? 200 : 0,
        latencyMs: 42,
        authorityHash: "a",
        nativeHash: "b",
        directoryHash: "c",
        certificateExpiresAt: calls === 1 ? "2026-12-31T12:00:00Z" : undefined,
        certificateIssuer: calls === 1 ? "Test Issuer" : undefined,
        message:
          calls === 1 ? undefined : "DNS points to an unexpected address.",
      };
      await route.fulfill({ json: report });
    },
  );
  await page.goto("/applications/audit-app");
  await page.getByRole("tab", { name: "Endpoints", exact: true }).click();
  await expect(
    page.getByText(
      "Not checked. Current membership does not confirm DNS, TLS or endpoint readiness.",
    ),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Verify Endpoint", exact: true })
    .click();
  await expect(
    page.getByText(
      "DNS: Expected Server Addresses · TLS: Certificate Valid · Readiness: HTTP 200",
      { exact: true },
    ),
  ).toBeVisible();
  await expect(
    page.getByText("Certificate expires", { exact: false }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Verify Endpoint", exact: true })
    .click();
  await expect(
    page.getByText("Verification Failed", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByText("DNS points to an unexpected address.", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Membership Current", { exact: false }),
  ).toBeVisible();
});

test("routing review explains unhealthy exclusions and healthy count", async ({
  page,
}) => {
  await fixtures(page);
  const member = (ordinal: number, excluded: boolean) => ({
    ordinal,
    resourceId: `resource-${ordinal}`,
    url: `https://backend-${ordinal}.example.test`,
    retired: false,
    excluded,
    status: excluded ? "running:unhealthy" : "running:healthy",
  });
  const members = [member(1, false), member(2, true)];
  const plan = {
    id: "health-plan",
    name: "oap-route",
    component: "web",
    url: "https://shared.example.test",
    expectedVersion: 3,
    authorityHash: "a".repeat(64),
    directoryHash: "d".repeat(64),
    members,
    beforeHash: "b".repeat(64),
    desiredHash: "c".repeat(64),
    desiredConfig: "{}",
    added: [],
    removed: [members[1].url],
    state: "prepared",
    createdAt: "2026-10-11T12:00:00Z",
  };
  await page.route("**/api/v1/applications/audit-app/routes", (route) =>
    route.fulfill({
      json: {
        endpoints: [
          {
            name: plan.name,
            component: "web",
            url: plan.url,
            status: "update-needed",
            current: members.map((m) => m.url),
            desired: [members[0].url],
            excluded: [members[1]],
          },
        ],
        publications: [],
      },
    }),
  );
  await page.route("**/api/v1/applications/audit-app/route-reviews", (route) =>
    route.fulfill({ status: 201, json: plan }),
  );
  await page.goto("/applications/audit-app");
  await page.getByRole("tab", { name: "Endpoints", exact: true }).click();
  await expect(
    page.getByText("1 instance excluded", { exact: false }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Review Routing", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByText("1 healthy backends", { exact: false }),
  ).toBeVisible();
  await expect(
    dialog.getByText("Instance 2: running:unhealthy · Excluded from Routing"),
  ).toBeVisible();
  await expect(dialog.getByText(members[1].url, { exact: true })).toBeVisible();
});

test("automatic membership requires owner opt-in and can be disabled", async ({
  page,
}) => {
  await fixtures(page);
  let mode: "manual" | "automatic" | "paused" = "manual";
  await page.route("**/api/v1/applications/audit-app/routes", (route) =>
    route.fulfill({
      json: {
        endpoints: [
          {
            name: "oap-route",
            component: "web",
            url: "https://shared.example.test",
            status: "in-sync",
            current: ["https://backend-1.example.test"],
            desired: ["https://backend-1.example.test"],
            excluded: [],
            automation:
              mode === "manual"
                ? undefined
                : {
                    enabled: mode === "automatic",
                    status: mode === "paused" ? "paused" : "in-sync",
                    message:
                      mode === "paused"
                        ? "Native route drift detected. No automatic changes were made."
                        : "",
                    checkedAt: "2026-10-11T12:00:00Z",
                  },
          },
        ],
        publications: [],
      },
    }),
  );
  await page.route(
    "**/api/v1/applications/audit-app/route-automation",
    async (route) => {
      const body = route.request().postDataJSON();
      expect(body.name).toBe("oap-route");
      expect(body.expectedVersion).toBe(3);
      mode = body.enabled ? "automatic" : "manual";
      await route.fulfill({
        json: {
          enabled: body.enabled,
          status: body.enabled ? "in-sync" : "disabled",
          message: "",
          checkedAt: "2026-10-11T12:00:00Z",
        },
      });
    },
  );
  await page.goto("/applications/audit-app");
  await page.getByRole("tab", { name: "Endpoints", exact: true }).click();
  await expect(
    page.getByRole("button", { name: "Review Routing", exact: true }),
  ).toBeEnabled();
  await page
    .getByRole("button", { name: "Enable Automatic Updates", exact: true })
    .click();
  const dialog = page.getByRole("dialog");
  await expect(
    dialog.getByText("Native edits pause automation.", { exact: false }),
  ).toBeVisible();
  await page.screenshot({
    path: "../.local/ui-automation-consent.png",
    fullPage: true,
  });
  await dialog
    .getByRole("button", { name: "Enable Automatic Updates", exact: true })
    .click();
  await expect(
    page.getByText("Automatic · Every Minute", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Review Routing", exact: true }),
  ).toBeDisabled();
  await page.screenshot({
    path: "../.local/ui-automation-enabled.png",
    fullPage: true,
  });
  await page
    .getByRole("button", { name: "Disable Automatic Updates", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Review Routing", exact: true }),
  ).toBeEnabled();
  mode = "paused";
  await page.reload();
  await page.getByRole("tab", { name: "Endpoints", exact: true }).click();
  await expect(
    page.getByText("Paused · Owner Review Required", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(
      "Native route drift detected. No automatic changes were made.",
    ),
  ).toBeVisible();
});

test("target outage evidence survives navigation and rechecks never retry operations", async ({ page }) => {
  await fixtures(page);
  let checks=0;
  let state: {resourceCount:number;state:string;observedState:string;message:string;nativeSourceObservation:boolean;heldOperations:{id:string;applicationId:string}[];expiresAt?:string}={resourceCount:0,state:"unknown",observedState:"unknown",message:"Connection has not been checked for this configuration.",nativeSourceObservation:false,heldOperations:[] as {id:string;applicationId:string}[]};
  await page.route("**/api/v1/targets/audit-target/connection-state",route=>route.fulfill({json:state}));
  await page.route("**/api/v1/targets/audit-target/connection-checks",route=>{
    checks++;
    state=checks===1 ? {resourceCount:0,state:"unreachable",observedState:"unreachable",message:"The operator could not be reached within the connection deadline.",nativeSourceObservation:false,heldOperations:[{id:"held-operation",applicationId:app.id}]} : {resourceCount:0,state:"connected",observedState:"connected",message:"Connection verified.",nativeSourceObservation:false,heldOperations:[{id:"held-operation",applicationId:app.id}]};
    return route.fulfill({json:state});
  });
  await page.goto("/targets");
  await expect(page.getByText("Unknown",{exact:true})).toBeVisible();
  await page.getByRole("button",{name:"Verify Connection",exact:true}).click();
  await expect(page.getByText("Unreachable",{exact:true})).toBeVisible();
  await expect(page.getByRole("link",{name:"Review Operation",exact:true})).toHaveAttribute("href",`/applications/${app.id}?tab=deployments#operation-held-operation`);
  await page.reload();
  await expect(page.getByText("Unreachable",{exact:true})).toBeVisible();
  expect(checks).toBe(1);
  await page.screenshot({path:"../.local/ui-target-outage.png",fullPage:true});
  await page.getByRole("button",{name:"Verify Connection",exact:true}).click();
  await expect(page.getByText("Connected",{exact:true})).toBeVisible();
  await expect(page.getByRole("link",{name:"Review Operation",exact:true})).toBeVisible();
  expect(checks).toBe(2);
  state={...state,state:"connected",expiresAt:"2000-01-01T00:00:00Z",message:"Connection verified."};
  await page.reload();
  await expect(page.getByText("Stale",{exact:true})).toBeVisible();
  expect(checks).toBe(2);
});

test("metadata backup results keep coverage, failures and pending attempts distinct", async ({page})=>{
 await fixtures(page);
 const report={version:1,id:"a".repeat(32),action:"backup",status:"succeeded",source:"host-helper",coverage:"metadata-only",startedAt:"2026-10-13T10:00:00Z",updatedAt:"2026-10-13T10:01:00Z",sha256:"b".repeat(64),sizeBytes:1024,errorCode:""};
 await page.route("**/api/v1/access/backups",route=>route.fulfill({json:{configured:true,invalidReports:0,truncated:false,reports:[report,{...report,id:"c".repeat(32),action:"restore-check",status:"failed",sha256:"",sizeBytes:0,errorCode:"checksum-mismatch"},{...report,id:"d".repeat(32),status:"running",sha256:"",sizeBytes:0}]}}));
 await page.goto("/access");await page.getByRole("tab",{name:"Backups",exact:true}).click();
 await expect(page.getByText("Application Data Excluded",{exact:true})).toBeVisible();
 await expect(page.getByText("Configuration & Keys: Separate Backup Required",{exact:true})).toBeVisible();
 await expect(page.getByText("Archive checksum or format did not match.",{exact:true})).toBeVisible();
 await expect(page.getByText("Result Pending",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Refresh Reports",exact:true})).toBeVisible();
 await page.screenshot({path:"../.local/ui-backup-reports.png",fullPage:true});
 await page.getByRole("button",{name:"Backup & Recovery Steps",exact:true}).click();
 await expect(page.getByText(/Retention: keep at least two verified archives/)).toBeVisible();
 await page.setViewportSize({width:390,height:844});
 expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});

test("encrypted store status exposes grant metadata and recovery without values",async({page})=>{
 await fixtures(page);
 await page.route("**/api/v1/access/secret-store",route=>route.fulfill({json:{enabled:true,state:"ready",loadedAt:"2026-10-13T12:00:00Z",grants:[{reference:"token-v1",targetId:"audit-target",applicationId:app.id,component:"web",variable:"TOKEN"}]}}));
 await page.goto("/access");await page.getByRole("tab",{name:"Secrets",exact:true}).click();
 await expect(page.getByText("Ready · 1 scoped grants loaded at startup.",{exact:true})).toBeVisible();
 await expect(page.getByText("token-v1",{exact:true})).toBeVisible();
 await page.getByRole("button",{name:"Key Backup & Recovery",exact:true}).click();
 await expect(page.getByText(/Save the encrypted store and its original 32-byte key separately/)).toBeVisible();
 await page.setViewportSize({width:390,height:844});expect(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.screenshot({path:"../.local/ui-encrypted-store.png",fullPage:true});
});

test("owned Docker security exceptions are explicit and inherited controls stay native",async({page})=>{
 await fixtures(page);
 await page.route("**/api/v1/targets/audit-target/capabilities",route=>route.fulfill({json:{standard:true,containerSecurity:true,environment:true,namedVolumes:true,resourceLimits:true}}));
 await page.goto(`/applications/${app.id}`);await page.getByRole("tab",{name:"Settings",exact:true}).click();
 const policy=page.locator('[data-slot="collapsible"]').filter({has:page.getByRole("button",{name:"Container Security",exact:true})}).first();
 await policy.getByRole("button",{name:"Container Security",exact:true}).click();
 await expect(policy.getByText("No New Privileges",{exact:true})).toBeVisible();
 await policy.getByLabel("Allow Privilege Escalation",{exact:true}).check();
 await expect(policy.getByLabel("Exception Reason",{exact:true})).toBeVisible();
 await policy.getByLabel("Exception Reason",{exact:true}).fill("Reviewed test fixture setuid requirement");
 await expect(policy.getByText("Owner Exception",{exact:true})).toBeVisible();
 await page.screenshot({path:"../.local/ui-container-security.png",fullPage:true});
});
