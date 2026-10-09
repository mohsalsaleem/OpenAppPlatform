import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./tests",
  timeout: 30000,
  use: {
    baseURL: process.env.OAP_BASE_URL || "http://127.0.0.1:8787",
    headless: true,
  },
  workers: 1,
  reporter: "list",
});
