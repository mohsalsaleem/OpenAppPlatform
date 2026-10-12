import { useState, useEffect, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from "@/components/ui/collapsible";
import { Button } from "@/components/ui/button";

export function Disclosure({
  title,
  children,
  className,
  defaultOpen = false,
}: {
  title: ReactNode;
  children: ReactNode;
  className?: string;
  defaultOpen?: boolean;
}) {
  const [open, setOpen] = useState(defaultOpen);
  useEffect(() => setOpen(defaultOpen), [defaultOpen]);
  return (
    <Collapsible className={className} open={open} onOpenChange={setOpen}>
      <CollapsibleTrigger asChild>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          className="group h-auto min-h-8 w-full justify-start px-0 py-1.5 text-left whitespace-normal text-muted-foreground hover:bg-transparent hover:text-foreground"
        >
          <ChevronDown className="transition-transform group-data-[state=open]:rotate-180" />
          <span className="min-w-0 [overflow-wrap:anywhere]">{title}</span>
        </Button>
      </CollapsibleTrigger>
      <CollapsibleContent
        forceMount
        className="pt-3 data-[state=closed]:hidden"
      >
        {children}
      </CollapsibleContent>
    </Collapsible>
  );
}
