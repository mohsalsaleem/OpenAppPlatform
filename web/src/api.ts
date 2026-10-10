export interface Component {
  management?: "observe";
  serviceEndpoints?: Record<string, string>;
  env?: Record<string, string>;
  services?: Record<string, string>;
  name: string;
  kind: string;
  image: string;
  port: number;
  hostPort?: number;
  instances: number;
  strategy: string;
  resourceId?: string;
}
export interface Manifest {
  name: string;
  environment: string;
  targetId: string;
  components: Component[];
}
export interface ApplicationGroup {
  id: string;
  name: string;
  version: number;
  environments: Application[];
}
export interface Application {
  groupId: string;
  id: string;
  manifest: Manifest;
  createdAt: string;
  updatedAt: string;
  version: number;
}
export interface Target {
  id: string;
  name: string;
  operator: string;
  environment: string;
  url: string;
}
export interface Step {
  component: string;
  ordinal: number;
  phase: string;
  action?: "restart" | "retire";
  recoveryPhase?: string;
  resourceId?: string;
  remoteDeploymentId?: string;
  observed?: string;
  error?: string;
}
export interface Deployment {
  source?: {
    repository: string;
    commit: string;
    ref: string;
    deliveryId: string;
  };
  operation?: "deploy" | "restart" | "scale-down";
  id: string;
  applicationId: string;
  definitionVersion: number;
  state: string;
  manifest: Manifest;
  steps: Step[];
  createdAt: string;
  updatedAt: string;
}
export interface Resource {
  description?: string;
  applicationId?: string;
  component?: string;
  id: string;
  name: string;
  status: string;
  artifactKind?: string;
  port?: number;
  image?: string;
  url?: string;
}
export const token = () => sessionStorage.getItem("oap-token") || "";
export async function api<T>(
  path: string,
  options: RequestInit = {},
): Promise<T> {
  const res = await fetch("/api/v1" + path, {
    ...options,
    headers: {
      "Content-Type": "application/json",
      ...(token() ? { Authorization: `Bearer ${token()}` } : {}),
      "X-OAP-CSRF": "1",
      ...options.headers,
    },
  });
  const data = await res.json();
  if (!res.ok) {
    if (res.status === 401 && !path.startsWith("/auth/"))
      window.dispatchEvent(new Event("oap-session-expired"));
    throw new Error(data.message || "Request failed");
  }
  return data;
}

export interface Instance {
  retired: boolean;
  component: string;
  ordinal: number;
  resourceId?: string;
  status: string;
  resource?: Resource;
  error?: string;
  checkedAt: string;
}

export interface Capabilities {
  managementHandoff: boolean;
  standard: boolean;
  serviceEndpoints: boolean;
  retirement: boolean;
  restart: boolean;
  environment: boolean;
  applicationDns: boolean;
}
