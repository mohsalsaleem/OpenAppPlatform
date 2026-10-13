import { test, expect } from "@playwright/test";
import { readFile, writeFile } from "node:fs/promises";

// A separate owner-mode installation built from the shipping Dockerfile.
// Its daemon/database and credentials are disposable fixtures, not developer state.
test("fresh packaged installation supports the complete owner application journey", async ({
  page,
}) => {
  test.setTimeout(90000);
  const phase = process.env.OAP_INSTALL_PHASE!;
  const receipt = process.env.OAP_INSTALL_RECEIPT!;
  await page.goto("/");
  if (phase === "bootstrap") {
    await expect(
      page.getByRole("heading", { name: "Set Up Your Workspace" }),
    ).toBeVisible();
    await page.screenshot({
      path: process.env.OAP_INSTALL_REPORT + "/install-owner-setup.png",
      fullPage: true,
    });
    await page.getByLabel("Your Name").fill("Installation owner");
    await page
      .getByLabel("Email", { exact: true })
      .fill("install@example.invalid");
    await page
      .getByLabel("Password", { exact: true })
      .fill("installation-owner-fixture-password");
    await page.getByLabel("Setup Secret").fill(process.env.OAP_SETUP_TOKEN!);
    await page.getByRole("button", { name: "Create Owner Account" }).click();
    await expect(
      page.getByRole("heading", { name: "Applications", exact: true }),
    ).toBeVisible();
    const connection = await page.request.get(
      "/api/v1/targets/docker-local/connection",
    );
    expect(connection.status()).toBe(200);
    const response = await page.request.post("/api/v1/applications", {
      headers: { "X-OAP-CSRF": "1" },
      data: {
        name: "installation-example",
        environment: "staging",
        targetId: "docker-local",
        components: [
          {
            name: "frontend",
            kind: "web",
            image: process.env.OAP_TEST_APPLICATION_IMAGE,
            port: 8080,
            hostPort: Number(process.env.OAP_INSTALL_FRONTEND_PORT),
            instances: 1,
            strategy: "standard",
            env: { MODE: "frontend" },
            services: { UPSTREAM_URL: "api" },
            dependsOn: ["api"],
            readiness: { requireHealthy: true, timeoutSeconds: 120 },
          },
          {
            name: "api",
            kind: "web",
            image: process.env.OAP_TEST_APPLICATION_IMAGE,
            port: 8080,
            instances: 1,
            strategy: "standard",
            env: { MODE: "api" },
            secretRefs: { DATABASE_URL: "install-database-v1" },
            readiness: { requireHealthy: true, timeoutSeconds: 120 },
          },
          {
            name: "worker",
            kind: "worker",
            image: process.env.OAP_TEST_APPLICATION_IMAGE,
            port: 0,
            instances: 1,
            strategy: "standard",
            env: { MODE: "worker" },
            secretRefs: { DATABASE_URL: "install-database-v1" },
            readiness: { requireHealthy: true, timeoutSeconds: 120 },
          },
        ],
      },
    });
    expect(response.status()).toBe(201);
    const app = await response.json();
    const linked = await page.request.post(
      `/api/v1/applications/${app.id}/databases`,
      {
        headers: { "X-OAP-CSRF": "1" },
        data: {
          name: "database",
          resourceId: process.env.OAP_INSTALL_DATABASE_ID,
          endpoint: "postgresql://database:5432/example",
          expectedVersion: app.version,
          acknowledge: true,
        },
      },
    );
    expect(linked.status()).toBe(200);
    const definition = await linked.json();
    for (const component of definition.manifest.components)
      if (["api", "worker"].includes(component.name))
        component.dependsOn = ["database"];
    const updated = await page.request.put(`/api/v1/applications/${app.id}`, {
      headers: { "X-OAP-CSRF": "1" },
      data: {
        manifest: definition.manifest,
        expectedVersion: definition.version,
      },
    });
    expect(updated.status()).toBe(200);
    await writeFile(receipt, JSON.stringify(await updated.json()), {
      mode: 0o600,
    });
    return;
  }
  await expect(
    page.getByRole("heading", { name: "Welcome Back" }),
  ).toBeVisible();
  await page
    .getByLabel("Email", { exact: true })
    .fill("install@example.invalid");
  await page
    .getByLabel("Password", { exact: true })
    .fill("installation-owner-fixture-password");
  await page.getByRole("button", { name: "Sign In", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Applications", exact: true }),
  ).toBeVisible();
  const app = JSON.parse(await readFile(receipt, "utf8"));
  await page.goto(`/applications/${app.id}`);
  await expect(
    page.getByRole("heading", { name: "installation-example", exact: true }),
  ).toBeVisible();
  if (phase === "deploy") {
    await page.getByRole("button", { name: "Review Deployment" }).click();
    await page.getByRole("button", { name: "Deploy Now", exact: true }).click();
  }
  await expect(page.getByText("4/4 healthy", { exact: false })).toBeVisible({
    timeout: 60000,
  });
  const logs = await page.request.get(
    `/api/v1/applications/${app.id}/logs/worker`,
  );
  expect(logs.status()).toBe(200);
  expect(await logs.text()).toContain("worker database connected");
  const frontend = await page.request.get(
    `http://127.0.0.1:${process.env.OAP_INSTALL_FRONTEND_PORT}/upstream`,
  );
  expect(frontend.status()).toBe(200);
  expect((await frontend.json()).connected).toBe(true);
  if (phase === "deploy") {
    await expect
      .poll(
        async () => {
          const releases = await page.request.get(
            `/api/v1/applications/${app.id}/deployments`,
          );
          return (await releases.json())[0]?.state;
        },
        { timeout: 30000 },
      )
      .toBe("succeeded");
    const response = await page.request.post(
      `/api/v1/applications/${app.id}/restarts`,
      {
        headers: {
          "X-OAP-CSRF": "1",
          "Idempotency-Key": "installation-worker-restart",
        },
        data: { component: "worker", ordinal: 1, expectedVersion: app.version },
      },
    );
    expect(response.status()).toBe(202);
    const operation = await response.json();
    await expect
      .poll(
        async () => {
          const r = await page.request.get(
            `/api/v1/deployments/${operation.id}`,
          );
          return (await r.json()).state;
        },
        { timeout: 30000 },
      )
      .toBe("succeeded");
  }
  if (phase === "backup-reports") {
    await page.getByRole("link",{name:"Workspace Settings",exact:true}).click();
    await page.getByRole("tab",{name:"Backups",exact:true}).click();
    const reportsResponse=await page.request.get("/api/v1/access/backups");
    expect(reportsResponse.status()).toBe(200);
    const reports=await reportsResponse.json();
    expect(reports.configured).toBe(true);
    expect(reports.invalidReports).toBe(0);
    expect(reports.reports.some((r:any)=>r.action==="backup" && r.status==="succeeded" && r.sha256.length===64)).toBe(true);
    expect(reports.reports.some((r:any)=>r.action==="restore-check" && r.status==="succeeded")).toBe(true);
    expect(reports.reports.some((r:any)=>r.action==="restore-check" && r.status==="failed" && r.errorCode==="checksum-mismatch")).toBe(true);
    await expect(page.getByText("Application Data Excluded",{exact:true})).toBeVisible();
    await expect(page.getByText("Archive checksum or format did not match.",{exact:true})).toBeVisible();
    await page.screenshot({path:process.env.OAP_INSTALL_REPORT+"/install-backup-reports.png",fullPage:true});
  }
  if (phase !== "bootstrap") {
    const encrypted=await page.request.get("/api/v1/access/secret-store");
    expect(encrypted.status()).toBe(200);const metadata=await encrypted.json();
    expect(metadata.state).toBe("ready");expect(metadata.grants.some((g:any)=>g.reference==="stored-proof-v1")).toBe(true);
    expect(JSON.stringify(metadata)).not.toContain("installation-private-stored-token");
  }
  const definition = await page.request.get(`/api/v1/applications/${app.id}`);
  expect(JSON.stringify(await definition.json())).not.toContain(
    "postgres://fixture_reader:",
  );
  await page.screenshot({
    path: process.env.OAP_INSTALL_REPORT + `/install-${phase}.png`,
    fullPage: true,
  });
});
