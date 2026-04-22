import type { PolicyFinding } from '../../api';
import { cn } from '../../lib/utils';

// Findings are identical in shape across the Policy + Scan panels
// (both emit the same Finding struct server-side), so the list view
// lives in one place and both panels wrap it.
export interface FindingsListProps {
  findings: PolicyFinding[];
  // Empty-state message — lets the caller differentiate "no policies
  // ran" from "no violations found" without a second component.
  emptyMessage?: string;
  className?: string;
}

const severityStyles = {
  error: 'bg-destructive/15 text-destructive border-destructive/40',
  warning: 'bg-[hsl(42_85%_55%_/_0.15)] text-[hsl(42_85%_70%)] border-[hsl(42_85%_55%_/_0.45)]',
  info: 'bg-muted text-muted-foreground border-border',
} as const;

export function FindingsList({
  findings,
  emptyMessage = 'No findings.',
  className,
}: FindingsListProps) {
  if (findings.length === 0) {
    return (
      <div className={cn('p-6 text-center text-sm text-muted-foreground', className)}>
        {emptyMessage}
      </div>
    );
  }

  return (
    <ul className={cn('flex flex-col gap-2 p-2', className)}>
      {findings.map((f, i) => {
        const severityClass = severityStyles[f.severity] ?? severityStyles.info;
        return (
          <li
            key={`${f.engine}-${f.policy_id}-${i}`}
            className={cn(
              'flex flex-col gap-1 rounded-md border bg-card px-3 py-2 shadow-sm',
            )}
          >
            <div className="flex items-center gap-2 text-[11px]">
              <span
                className={cn(
                  'rounded px-1.5 py-0.5 font-mono font-bold uppercase tracking-wider border',
                  severityClass,
                )}
              >
                {f.severity}
              </span>
              <span className="font-mono text-muted-foreground">{f.engine}</span>
              {f.category && (
                <span className="font-mono text-muted-foreground/80">· {f.category}</span>
              )}
              {f.resource && (
                <span className="ml-auto truncate font-mono text-foreground/80" title={f.resource}>
                  {f.resource}
                </span>
              )}
            </div>
            <div className="text-sm font-medium text-foreground">{f.policy_name}</div>
            <div className="text-xs text-muted-foreground">{f.message}</div>
            {f.suggestion && (
              <div className="mt-1 border-l-2 border-primary/60 pl-2 text-xs italic text-primary/90">
                {f.suggestion}
              </div>
            )}
          </li>
        );
      })}
    </ul>
  );
}
