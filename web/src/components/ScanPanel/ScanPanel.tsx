import { useEffect, useState } from 'react';
import { AlertCircle, Play, Shield } from 'lucide-react';

import { api, type ScanRunResponse } from '../../api';
import { Button } from '../ui/button';
import { FindingsList } from '../Findings/FindingsList';

export interface ScanPanelProps {
  projectName: string;
  tool?: string;
  client?: Pick<typeof api, 'listSecurityScanners' | 'runScanners'>;
}

// ScanPanel mirrors PolicyStudio for the security scanners (Checkov,
// Trivy, Terrascan, KICS). The backend emits the same Finding shape so
// rendering reuses FindingsList.
export function ScanPanel({ projectName, tool = 'terraform', client = api }: ScanPanelProps) {
  const [scanners, setScanners] = useState<{ name: string; available: boolean }[]>([]);
  const [selected, setSelected] = useState<Record<string, boolean>>({});
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ScanRunResponse | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    client.listSecurityScanners().then((list) => {
      setScanners(list);
      setSelected(Object.fromEntries(list.map((s) => [s.name, s.available])));
    }).catch((err) => setError(String(err)));
  }, [client]);

  const run = async () => {
    setRunning(true);
    setError(null);
    try {
      const names = scanners.filter((s) => selected[s.name]).map((s) => s.name);
      const response = await client.runScanners(projectName, { scanners: names, tool });
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
        <Shield className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Security Scan
        </h2>
        <Button
          size="sm"
          className="ml-auto"
          onClick={run}
          disabled={running || scanners.every((s) => !selected[s.name])}
        >
          <Play className="h-3.5 w-3.5" />
          {running ? 'Scanning...' : 'Run scanners'}
        </Button>
      </header>

      <section className="flex flex-wrap gap-2">
        {scanners.map((s) => (
          <label
            key={s.name}
            className={`flex cursor-pointer items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-xs ${s.available ? '' : 'opacity-60'}`}
          >
            <input
              type="checkbox"
              checked={!!selected[s.name]}
              disabled={!s.available}
              onChange={(ev) =>
                setSelected((prev) => ({ ...prev, [s.name]: ev.target.checked }))
              }
              aria-label={`Toggle ${s.name}`}
            />
            <span className="font-mono text-foreground">{s.name}</span>
            {!s.available && <span className="text-muted-foreground">(not installed)</span>}
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
            {result.results.length} scanner{result.results.length === 1 ? '' : 's'}
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
          emptyMessage={result ? 'No vulnerabilities found.' : 'Run scanners to see findings.'}
        />
      </div>
    </div>
  );
}
