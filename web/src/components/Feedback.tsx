export function ErrorBox({ error }: { error: unknown }) {
  return error ? (
    <div role="alert" className="error">
      {typeof error === "string"
        ? error
        : error instanceof Error
          ? error.message
          : "Something went wrong"}
    </div>
  ) : null;
}
export function Loading() {
  return (
    <p className="muted" role="status">
      Loading your workspace…
    </p>
  );
}
