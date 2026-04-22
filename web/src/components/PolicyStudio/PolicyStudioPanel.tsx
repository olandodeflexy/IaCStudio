import { useEffect, useState } from 'react';
import { AlertCircle, Play, ShieldCheck } from 'lucide-react';

import { api, type PolicyRunResponse } from '../../api';
import { Button } from '../ui/button';
import { FindingsList } from '../Findings/FindingsList';

export interface PolicyStudioPanelProps {
  projectName: string;
  tool?: string;
  // Allow the host to swap the client — we pass the default `api`
  // module but tests inject a stub that resolves canned responses.
  client?: Pick<typeof api, 'listPolicyEngines' | 'runPolicy'>;
}

// PolicyStudioPanel drives the /api/policy endpoints. It lists the
// registered engines (with their availability), lets the user toggle
// any subset on/off, fires the run, and renders findings grouped by
// severity. Uses the shared FindingsList so the look is identical to
// the Scan panel.
export function PolicyStudioPanel({
  projectName,
  tool = 'terraform',
  client = api,
}: PolicyStudioPanelProps) {
  const [engines, setEngines] = useState<{ name: string; available: boolean }[]>([]);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<PolicyRunResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    // Guard state setters against unmount so navigating away mid-fetch
    // doesn't trigger React's "setState on unmounted component" warning
    // (and worse, a ghost render after the panel is gone).
    let cancelled = false;
    client.listPolicyEngines().then((engs) => {
      if (cancelled) return;
      setEngines(engs);
      // Default: every available engine checked, unavailable ones off.
      setSelected(
        Object.fromEntries(engs.map((e) => [e.name, e.available])),
      );
    }).catch((err) => {
      if (cancelled) return;
      setError(String(err));
    });
    return () => {
      cancelled = true;
    };
  }, [client]);

  const run = async () => {
    setRunning(true);
    setError(null);
    try {
      const names = engines.filter((e) => selected[e.name]).map((e) => e.name);
      const response = await client.runPolicy(projectName, { engines: names, tool });
      setResult(response);
    } catch (err) {
      setError(String(err));
    } finally {
      setRunning(false);
    }
  };

  return (
    <div className="flex h-full flex-col gap-3 bg-background p-4">
      <header className="flex items-center gap-3">
        <ShieldCheck className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Policy Studio
        </h2>
        <Button
          size="sm"
          className="ml-auto"
          onClick={run}
          disabled={running || engines.every((e) => !selected[e.name])}
        >
          <Play className="h-3.5 w-3.5" />
          {running ? 'Running...' : 'Run policies'}
        </Button>
      </header>

      <section className="flex flex-wrap gap-2">
        {engines.map((e) => (
          <label
            key={e.name}
            className={`flex cursor-pointer items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-xs ${e.available ? '' : 'opacity-60'}`}
          >
            <input
              type="checkbox"
              checked={!!selected[e.name]}
              disabled={!e.available}
              onChange={(ev) =>
                setSelected((prev) => ({ ...prev, [e.name]: ev.target.checked }))
              }
              aria-label={`Toggle ${e.name}`}
            />
            <span className="font-mono text-foreground">{e.name}</span>
            {!e.available && <span className="text-muted-foreground">(not installed)</span>}
          </label>
        ))}
      </section>

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {result && (
        <div className="flex items-center justify-between rounded-md border border-border bg-card px-3 py-2 text-xs">
          <span className="font-mono text-muted-foreground">
            {result.findings.length} finding{result.findings.length === 1 ? '' : 's'} across{' '}
            {result.results.length} engine{result.results.length === 1 ? '' : 's'}
          </span>
          {result.blocking && (
            <span className="rounded bg-destructive/20 px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-widest text-destructive">
              Blocking
            </span>
          )}
        </div>
      )}

      <div className="flex-1 overflow-y-auto">
        <FindingsList
          findings={result?.findings ?? []}
          emptyMessage={result ? 'No violations 🎉' : 'Run policies to see findings.'}
        />
      </div>
    </div>
  );
}
