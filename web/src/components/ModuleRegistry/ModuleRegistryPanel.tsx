import { useEffect, useState } from 'react';
import { Check, Download, ExternalLink, Package, Search } from 'lucide-react';

import { api, type RegistryModule } from '../../api';
import { Button } from '../ui/button';
import { Input } from '../ui/input';

export interface ModuleRegistryPanelProps {
  client?: Pick<typeof api, 'searchModules'>;
  // Called when the user wants to adopt a module into their project
  // (e.g. drop a module block on the canvas). The parent decides what
  // 'adopt' means — insert an HCL block, open a scaffold dialog, etc.
  onAdopt?: (_module: RegistryModule) => void;
  initialQuery?: string;
}

// Debounce helper — useState + setTimeout is light enough not to warrant
// a dependency. Swap for a library hook if we grow more debounced
// inputs.
function useDebounced<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(t);
  }, [value, delayMs]);
  return debounced;
}

export function ModuleRegistryPanel({
  client = api,
  onAdopt,
  initialQuery = '',
}: ModuleRegistryPanelProps) {
  const [query, setQuery] = useState(initialQuery);
  const debouncedQuery = useDebounced(query.trim(), 250);
  const [modules, setModules] = useState<RegistryModule[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!debouncedQuery) {
      setModules([]);
      setError(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError(null);
    client.searchModules(debouncedQuery).then((result) => {
      if (cancelled) return;
      setModules(result.modules);
    }).catch((err) => {
      if (cancelled) return;
      setError(String(err));
    }).finally(() => {
      if (!cancelled) setLoading(false);
    });
    return () => {
      cancelled = true;
    };
  }, [debouncedQuery, client]);

  return (
    <div className="flex h-full flex-col gap-3 bg-background p-4">
      <header className="flex items-center gap-3">
        <Package className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Module Registry
        </h2>
      </header>

      <div className="relative">
        <Search className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search Terraform modules (e.g. vpc, eks, rds)..."
          className="pl-9"
          aria-label="Search modules"
        />
      </div>

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}

      <div className="flex-1 overflow-y-auto">
        {loading && (
          <div className="p-6 text-center text-xs text-muted-foreground">Searching…</div>
        )}
        {!loading && !error && modules.length === 0 && (
          <div className="p-6 text-center text-xs text-muted-foreground">
            {debouncedQuery ? 'No modules found.' : 'Start typing to search the registry.'}
          </div>
        )}
        <ul className="flex flex-col gap-2">
          {modules.map((m) => (
            <li
              key={m.id}
              className="flex flex-col gap-1 rounded-md border border-border bg-card px-3 py-2"
            >
              <div className="flex items-center gap-2">
                <a
                  href={m.source || '#'}
                  target="_blank"
                  rel="noreferrer"
                  className="truncate text-sm font-semibold text-foreground hover:text-primary"
                >
                  {m.namespace}/{m.name}/{m.provider}
                </a>
                {m.verified && (
                  <span
                    title="Verified by HashiCorp"
                    className="inline-flex items-center gap-0.5 rounded bg-primary/20 px-1 py-0.5 text-[9px] font-bold uppercase tracking-wider text-primary"
                  >
                    <Check className="h-2.5 w-2.5" /> verified
                  </span>
                )}
                <span className="ml-auto font-mono text-[10px] text-muted-foreground">
                  v{m.version}
                </span>
              </div>
              <div className="line-clamp-2 text-xs text-muted-foreground">{m.description}</div>
              <div className="mt-1 flex items-center gap-3 text-[10px] text-muted-foreground/80">
                <span className="inline-flex items-center gap-1 font-mono">
                  <Download className="h-3 w-3" />
                  {m.downloads.toLocaleString()}
                </span>
                {m.source && (
                  <a
                    className="inline-flex items-center gap-1 font-mono hover:text-foreground"
                    href={m.source}
                    target="_blank"
                    rel="noreferrer"
                  >
                    <ExternalLink className="h-3 w-3" />
                    source
                  </a>
                )}
                {onAdopt && (
                  <Button
                    size="sm"
                    variant="secondary"
                    className="ml-auto h-6 px-2 text-[10px]"
                    onClick={() => onAdopt(m)}
                  >
                    Adopt
                  </Button>
                )}
              </div>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
