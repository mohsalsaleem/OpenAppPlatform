import createClient from "openapi-fetch";
import type { paths } from "./generated/api";
import type { components } from "./generated/api";
export type HealthCheck = components["schemas"]["HealthCheck"];
export type Component = components["schemas"]["Component"];
export type Manifest = components["schemas"]["Manifest"];
export type ApplicationGroup = components["schemas"]["ApplicationGroup"];
export type Application = components["schemas"]["Application"];
export type Target = components["schemas"]["Target"];
export type Step = components["schemas"]["Step"];
export type Deployment = components["schemas"]["Deployment"];
export type Resource = components["schemas"]["Resource"];
export type Instance = components["schemas"]["Instance"];
export type Capabilities = components["schemas"]["Capabilities"];

export const token = () => sessionStorage.getItem("oap-token") || "";

export const api = createClient<paths>();
api.use({
  onRequest({ request }) {
    if (token() && !request.headers.has("Authorization"))
      request.headers.set("Authorization", `Bearer ${token()}`);
    request.headers.set("X-OAP-CSRF", "1");
    return request;
  },
  onResponse({ response }) {
    if (
      response.status === 401 &&
      !new URL(response.url, location.origin).pathname.startsWith(
        "/api/v1/auth/",
      )
    )
      window.dispatchEvent(new Event("oap-session-expired"));
  },
});
export function unwrap<T>({
  data,
  error,
  response,
}: {
  data?: T;
  error?: unknown;
  response: Response;
}): T {
  if (!response.ok)
    throw new Error(
      (error as { message?: string })?.message || "Request failed",
    );
  return data as T;
}

/** Feature state derives from the same generated response contract as requests. */
export type ReadResponse<P extends keyof paths> = paths[P] extends {
  get: { responses: { 200: { content: { "application/json": infer Body } } } };
}
  ? Body
  : never;
