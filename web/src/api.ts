export interface Component {
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
export interface Application {
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
  recoveryPhase?: string;
  resourceId?: string;
  remoteDeploymentId?: string;
  observed?: string;
  error?: string;
}
export interface Deployment {
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
      Authorization: `Bearer ${token()}`,
      ...options.headers,
    },
  });
  const data = await res.json();
  if (!res.ok) throw new Error(data.message || "Request failed");
  return data;
}

export interface Instance {
  component: string;
  ordinal: number;
  resourceId?: string;
  status: string;
  resource?: Resource;
  error?: string;
  checkedAt: string;
}
