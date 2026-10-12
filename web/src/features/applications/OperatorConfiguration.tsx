import { Button } from "@/components/ui/button";
import { ExternalLink } from "lucide-react";
import type { Component, Target } from "../../api";

export function OperatorConfiguration({
  component,
  target,
}: {
  component: Component;
  target?: Target;
}) {
  let operatorURL: string | undefined;
  if (target?.operator !== "docker" && target?.url) {
    try {
      const url = new URL(target.url);
      if (
        ["https:", "http:"].includes(url.protocol) &&
        !url.username &&
        !url.password &&
        !url.search &&
        !url.hash
      )
        operatorURL = url.href;
    } catch {
      /* A target without a web console has no external link. */
    }
  }
  return (
    <section
      aria-label={`Inherited Configuration for ${component.name}`}
      className="space-y-3"
    >
      <p className="small muted">
        Runtime configuration stays in {target?.name || "the existing platform"}
        . Make changes there.
      </p>
      {component.databaseEndpoint && (
        <code className="image-ref">{component.databaseEndpoint}</code>
      )}
      {operatorURL && (
        <Button asChild variant="outline" size="sm">
          <a href={operatorURL} target="_blank" rel="noopener noreferrer">
            <ExternalLink /> Open Operator
          </a>
        </Button>
      )}
    </section>
  );
}
