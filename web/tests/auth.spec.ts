import { test, expect } from "@playwright/test";
test("owner setup, sessions, invitations, read-only membership, and scoped agents", async ({
  page,
  browser,
}) => {
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "Set up your workspace" }),
  ).toBeVisible();
  await page.screenshot({
    path: "../.local/ui-owner-setup.png",
    fullPage: true,
  });
  await page.getByLabel("Your name").fill("Workspace owner");
  await page.getByLabel("Email", { exact: true }).fill("owner@example.invalid");
  await page
    .getByLabel("Password", { exact: true })
    .fill("owner-browser-password");
  await page.getByLabel("Setup secret").fill(process.env.OAP_SETUP_TOKEN!);
  await page.getByRole("button", { name: "Create owner account" }).click();
  await expect(
    page.getByRole("heading", { name: "Your applications" }),
  ).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Group existing services" }),
  ).toBeVisible();
  const stageResponse = await page.request.post("/api/v1/applications", {
    headers: { "X-OAP-CSRF": "1" },
    data: {
      name: "environment-browser-stage",
      environment: "staging",
      targetId: "test-docker",
      components: [
        {
          name: "web",
          image: "nginx:alpine",
          port: 80,
          instances: 1,
          strategy: "standard",
        },
      ],
    },
  });
  expect(stageResponse.status()).toBe(201);
  const stageApp = await stageResponse.json();
  const prodResponse = await page.request.post("/api/v1/applications", {
    headers: { "X-OAP-CSRF": "1" },
    data: {
      name: "environment-browser-production",
      environment: "production",
      targetId: "docker-production",
      components: [
        {
          name: "web",
          image: "nginx:alpine",
          port: 80,
          instances: 1,
          strategy: "standard",
        },
      ],
    },
  });
  expect(prodResponse.status()).toBe(201);
  const prodApp = await prodResponse.json();
  await page.goto(`/applications/${stageApp.id}`);
  await page.getByLabel("Existing environment").selectOption(prodApp.id);
  await page
    .getByRole("button", { name: "Link environment", exact: true })
    .click();
  await expect(
    page
      .getByRole("navigation", { name: "Application environments" })
      .getByRole("link", { name: "production" }),
  ).toBeVisible();
  await page
    .getByRole("navigation", { name: "Application environments" })
    .getByRole("link", { name: "production" })
    .click();
  await expect(page).toHaveURL(new RegExp(prodApp.id));
  await expect(
    page.getByRole("heading", {
      name: "Environments",
      exact: true,
    }),
  ).toBeVisible();
  await page.screenshot({
    path: "../.local/ui-environments.png",
    fullPage: true,
  });
  await page
    .locator("aside")
    .getByRole("link", { name: "Applications", exact: true })
    .click();
  await expect(
    page
      .locator(".application-group")
      .filter({ hasText: "environment-browser-stage" }),
  ).toContainText("2 environments");
  await page.screenshot({
    path: "../.local/ui-grouped-applications.png",
    fullPage: true,
  });
  await page.getByRole("link", { name: "Workspace access" }).click();
  await expect(
    page.getByRole("heading", { name: "Workspace access" }),
  ).toBeVisible();
  const headers = { "X-OAP-CSRF": "1" };
  const inviteResponse = await page.request.post("/api/v1/access/invitations", {
    headers,
    data: { email: "viewer@example.invalid", role: "viewer" },
  });
  expect(inviteResponse.status()).toBe(201);
  const invite = (await inviteResponse.json()).invite;
  const viewerContext = await browser.newContext();
  const viewer = await viewerContext.newPage();
  await viewer.goto(process.env.OAP_BASE_URL!);
  await viewer.getByRole("button", { name: "Have an invitation?" }).click();
  await viewer.getByLabel("Your name").fill("Read-only member");
  await viewer
    .getByLabel("Email", { exact: true })
    .fill("viewer@example.invalid");
  await viewer
    .getByLabel("Password", { exact: true })
    .fill("viewer-browser-password");
  await viewer.getByLabel("Invitation code").fill(invite);
  await viewer.getByRole("button", { name: "Accept invitation" }).click();
  await expect(
    viewer.getByRole("heading", { name: "Your applications" }),
  ).toBeVisible();
  await expect(
    viewer.getByRole("link", { name: "New application", exact: true }),
  ).not.toBeVisible();
  await expect(
    viewer.getByRole("link", { name: "Workspace access" }),
  ).not.toBeVisible();
  const apps = await (await page.request.get("/api/v1/applications")).json();
  expect(apps.length).toBeGreaterThan(1);
  const credentialResponse = await page.request.post(
    "/api/v1/access/credentials",
    {
      headers,
      data: {
        name: "Read-only agent",
        scope: "read",
        applicationId: apps[0].id,
      },
    },
  );
  expect(credentialResponse.status()).toBe(201);
  const token = (await credentialResponse.json()).token;
  expect(
    (
      await page.request.get(`/api/v1/applications/${apps[0].id}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
    ).status(),
  ).toBe(200);
  expect(
    (
      await page.request.get(`/api/v1/applications/${apps[1].id}`, {
        headers: { Authorization: `Bearer ${token}` },
      })
    ).status(),
  ).toBe(404);
  expect(
    (
      await page.request.post(
        `/api/v1/applications/${apps[0].id}/deployments`,
        { headers: { Authorization: `Bearer ${token}` }, data: {} },
      )
    ).status(),
  ).toBe(403);
  await viewer.goto(`${process.env.OAP_BASE_URL}/applications/${apps[0].id}`);
  await expect(
    viewer.getByRole("button", {
      name: /Deploy application|Observe-only application/,
    }),
  ).toBeDisabled();
  await viewer.getByRole("button", { name: "Sign out" }).click();
  await expect(
    viewer.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  await viewerContext.close();
  await page.reload();
  await expect(
    page.getByText("Read-only member", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("Read-only agent", { exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "../.local/ui-workspace-access.png",
    fullPage: true,
  });
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  await page.getByLabel("Email", { exact: true }).fill("owner@example.invalid");
  await page
    .getByLabel("Password", { exact: true })
    .fill("owner-browser-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Workspace access" }),
  ).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back" }),
  ).toBeVisible();
  await page.screenshot({
    path: "../.local/ui-sign-in-mobile.png",
    fullPage: true,
  });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBeTruthy();
});
