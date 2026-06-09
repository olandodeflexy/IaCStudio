import { useMemo, useState } from 'react';
import { AlertCircle, FileText, GitCompareArrows, GitPullRequest, Play, RotateCcw } from 'lucide-react';

import {
  api,
  type DriftFinding,
  type DriftRemediationArtifactSet,
  type DriftRemediationMode,
  type DriftRemediationProposal,
  type DriftReport,
} from '../../api';
import { Button } from '../ui/button';

export interface DriftPanelProps {
  projectName: string;
  tool?: string;
  env?: string;
  client?: Pick<typeof api, 'runDrift'> & Partial<Pick<typeof api, 'createDriftRemediation' | 'createDriftRemediationArtifacts'>>;
}

const SUPPORTED_TOOLS = new Set(['terraform', 'opentofu']);

const CLASSIFICATION_STYLES: Record<string, string> = {
  legitimate: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300',
  legitimate_config_change: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300',
  unauthorized: 'border-destructive/40 bg-destructive/10 text-destructive',
  unauthorized_change: 'border-destructive/40 bg-destructive/10 text-destructive',
  unmanaged: 'border-amber-500/30 bg-amber-500/10 text-amber-300',
  missing: 'border-amber-500/30 bg-amber-500/10 text-amber-300',
  missing_from_state: 'border-amber-500/30 bg-amber-500/10 text-amber-300',
  unknown: 'border-border bg-card text-muted-foreground',
};

function formatValue(value: unknown): string {
  if (value === undefined) return 'not set';
  if (value === null) return 'null';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    return JSON.stringify(value);
  } catch {
    return String(value);
  }
}

function classificationStyle(classification: string): string {
  return CLASSIFICATION_STYLES[classification.toLowerCase()] ?? CLASSIFICATION_STYLES.unknown;
}

function formatLabel(value: string): string {
  return value.replace(/_/g, ' ');
}

function normalizeFindings(report: DriftReport | null): DriftFinding[] {
  return Array.isArray(report?.findings) ? report.findings : [];
}

function normalizeSuppressedFindings(report: DriftReport | null): DriftFinding[] {
  return Array.isArray(report?.suppressed_findings) ? report.suppressed_findings : [];
}

function normalizeClassifications(report: DriftReport | null): [string, number][] {
  if (!report?.classifications) return [];
  return Object.entries(report.classifications)
    .filter(([, count]) => count > 0)
    .sort(([left], [right]) => left.localeCompare(right));
}

export function DriftPanel({
  projectName,
  tool = 'terraform',
  env,
  client = api,
}: DriftPanelProps) {
  const [running, setRunning] = useState(false);
  const [draftingMode, setDraftingMode] = useState<DriftRemediationMode | null>(null);
  const [writingArtifacts, setWritingArtifacts] = useState(false);
  const [report, setReport] = useState<DriftReport | null>(null);
  const [proposal, setProposal] = useState<DriftRemediationProposal | null>(null);
  const [artifacts, setArtifacts] = useState<DriftRemediationArtifactSet | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [proposalError, setProposalError] = useState<string | null>(null);
  const [artifactError, setArtifactError] = useState<string | null>(null);
  const supported = SUPPORTED_TOOLS.has(tool);
  const findings = useMemo(() => normalizeFindings(report), [report]);
  const suppressedFindings = useMemo(() => normalizeSuppressedFindings(report), [report]);
  const classifications = useMemo(() => normalizeClassifications(report), [report]);
  const canDraftRemediation = supported && findings.length > 0 && Boolean(client.createDriftRemediation);

  const run = async () => {
    if (!supported) return;
    setRunning(true);
    setError(null);
    setProposalError(null);
    setArtifactError(null);
    setReport(null);
    setProposal(null);
    setArtifacts(null);
    try {
      const req: { tool: string; env?: string } = { tool };
      if (env) req.env = env;
      const response = await client.runDrift(projectName, req);
      setReport(response);
    } catch (err) {
      setError(String(err));
    } finally {
      setRunning(false);
    }
  };

  const draftRemediation = async (mode: DriftRemediationMode) => {
    if (!canDraftRemediation || !client.createDriftRemediation) return;
    setDraftingMode(mode);
    setProposalError(null);
    setArtifactError(null);
    setProposal(null);
    setArtifacts(null);
    try {
      const req: { tool: string; env?: string; mode: DriftRemediationMode } = { tool, mode };
      if (env) req.env = env;
      const response = await client.createDriftRemediation(projectName, req);
      setProposal(response);
    } catch (err) {
      setProposalError(String(err));
    } finally {
      setDraftingMode(null);
    }
  };

  const writeArtifacts = async () => {
    if (!proposal || !client.createDriftRemediationArtifacts) return;
    setWritingArtifacts(true);
    setArtifactError(null);
    setArtifacts(null);
    try {
      const req: { tool: string; env?: string; mode: DriftRemediationMode } = { tool, mode: proposal.mode };
      if (env) req.env = env;
      const response = await client.createDriftRemediationArtifacts(projectName, req);
      setArtifacts(response);
    } catch (err) {
      setArtifactError(String(err));
    } finally {
      setWritingArtifacts(false);
    }
  };

  return (
    <div className="flex h-full flex-col gap-3 bg-background p-4">
      <header className="flex items-center gap-3">
        <GitCompareArrows className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Drift Monitor
        </h2>
        <Button
          size="sm"
          className="ml-auto"
          onClick={run}
          disabled={running || !supported}
        >
          <Play className="h-3.5 w-3.5" />
          {running ? 'Checking...' : 'Run drift'}
        </Button>
      </header>

      {!supported && (
        <div className="flex items-start gap-2 rounded-md border border-border bg-card px-3 py-2 text-xs text-muted-foreground">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
          <span>Drift detection currently supports Terraform and OpenTofu projects.</span>
        </div>
      )}

      {error && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
          <span>{error}</span>
        </div>
      )}

      {report && (
        <section className="rounded-md border border-border bg-card px-3 py-3">
          <div className="flex items-start justify-between gap-3">
            <div>
              <div className="text-xs font-mono text-muted-foreground">{report.summary}</div>
              {report.state_path && (
                <div className="mt-1 truncate text-[10px] font-mono text-muted-foreground">
                  state {report.state_path}
                </div>
              )}
            </div>
            <div className="flex flex-shrink-0 flex-col items-end gap-1">
              <span className={`rounded border px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-widest ${findings.length > 0 ? 'border-amber-500/30 bg-amber-500/10 text-amber-300' : 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300'}`}>
                {findings.length} finding{findings.length === 1 ? '' : 's'}
              </span>
              {suppressedFindings.length > 0 && (
                <span className="rounded border border-border bg-background/70 px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-widest text-muted-foreground">
                  {suppressedFindings.length} suppressed
                </span>
              )}
            </div>
          </div>
          {classifications.length > 0 && (
            <div className="mt-3 flex flex-wrap gap-2">
              {classifications.map(([classification, count]) => (
                <span
                  key={classification}
                  className={`rounded border px-2 py-1 font-mono text-[10px] uppercase tracking-widest ${classificationStyle(classification)}`}
                >
                  {formatLabel(classification)} {count}
                </span>
              ))}
            </div>
          )}
          {findings.length > 0 && (
            <div className="mt-3 grid grid-cols-2 gap-2">
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => draftRemediation('codify')}
                disabled={!canDraftRemediation || draftingMode !== null}
              >
                <GitPullRequest className="h-3.5 w-3.5" />
                {draftingMode === 'codify' ? 'Drafting...' : 'Draft codify PR'}
              </Button>
              <Button
                type="button"
                variant="outline"
                size="sm"
                onClick={() => draftRemediation('revert')}
                disabled={!canDraftRemediation || draftingMode !== null}
              >
                <RotateCcw className="h-3.5 w-3.5" />
                {draftingMode === 'revert' ? 'Drafting...' : 'Draft revert PR'}
              </Button>
            </div>
          )}
        </section>
      )}

      {proposalError && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
          <span>{proposalError}</span>
        </div>
      )}

      {proposal && (
        <section className="rounded-md border border-primary/30 bg-primary/10 px-3 py-3">
          <div className="flex items-start gap-2">
            <GitPullRequest className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-primary" />
            <div className="min-w-0 flex-1">
              <div className="truncate text-xs font-semibold text-foreground">{proposal.title}</div>
              <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
                branch {proposal.branch}
              </div>
              <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
                commit {proposal.commit_message}
              </div>
            </div>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={writeArtifacts}
              disabled={writingArtifacts || !client.createDriftRemediationArtifacts}
            >
              <FileText className="h-3.5 w-3.5" />
              {writingArtifacts ? 'Writing...' : 'Write artifacts'}
            </Button>
          </div>

          {proposal.file_changes.length > 0 && (
            <div className="mt-3 space-y-2">
              {proposal.file_changes.slice(0, 3).map((change, index) => (
                <div
                  key={`${change.address}-${change.field ?? change.action}-${index}`}
                  className="rounded border border-border bg-background/70 px-2 py-2"
                >
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate text-xs font-semibold text-foreground">{change.address}</span>
                    <span className="flex-shrink-0 rounded border border-border px-2 py-0.5 font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
                      {change.action}
                    </span>
                  </div>
                  <div className="mt-1 text-xs leading-5 text-muted-foreground">{change.summary}</div>
                  {(change.path || change.field) && (
                    <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
                      {change.path ?? 'manual'}{change.line ? `:${change.line}` : ''}{change.field ? ` / ${change.field}` : ''}
                    </div>
                  )}
                </div>
              ))}
              {proposal.file_changes.length > 3 && (
                <div className="text-[10px] text-muted-foreground">
                  +{proposal.file_changes.length - 3} more change{proposal.file_changes.length - 3 !== 1 ? 's' : ''} — see PR description
                </div>
              )}
            </div>
          )}

          {proposal.warnings && proposal.warnings.length > 0 && (
            <div className="mt-3 space-y-1 rounded border border-amber-500/30 bg-amber-500/10 px-2 py-2 text-xs leading-5 text-amber-200">
              {proposal.warnings.map((warning, i) => (
                <div key={i}>{warning}</div>
              ))}
            </div>
          )}
        </section>
      )}

      {artifactError && (
        <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 flex-shrink-0" />
          <span>{artifactError}</span>
        </div>
      )}

      {artifacts && (
        <section className="rounded-md border border-border bg-card px-3 py-3">
          <div className="flex items-start gap-2">
            <FileText className="mt-0.5 h-3.5 w-3.5 flex-shrink-0 text-primary" />
            <div className="min-w-0">
              <div className="text-xs font-semibold text-foreground">Review artifacts written</div>
              <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
                {artifacts.root}
              </div>
            </div>
          </div>
          <div className="mt-3 space-y-1">
            {artifacts.files.map((file) => (
              <div
                key={file.path}
                className="flex items-center justify-between gap-2 rounded border border-border bg-background/70 px-2 py-1.5"
              >
                <span className="truncate font-mono text-[10px] text-foreground">{file.path}</span>
                <span className="flex-shrink-0 font-mono text-[10px] uppercase tracking-widest text-muted-foreground">{file.kind}</span>
              </div>
            ))}
          </div>
        </section>
      )}

      <div className="flex-1 overflow-y-auto">
        {!report && (
          <div className="rounded-md border border-dashed border-border bg-card/40 px-3 py-8 text-center text-xs text-muted-foreground">
            Run drift to compare Terraform/OpenTofu state against the current code.
          </div>
        )}

        {report && findings.length === 0 && (
          <div className="rounded-md border border-border bg-card px-3 py-8 text-center text-xs text-muted-foreground">
            {report.has_state ? 'No active drift findings.' : 'No state file was found for this project.'}
          </div>
        )}

        {findings.length > 0 && (
          <div className="space-y-3">
            {findings.map((finding, index) => (
              <article
                key={`${finding.address}-${finding.path ?? finding.status}-${index}`}
                className="rounded-md border border-border bg-card p-3"
              >
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <div className="truncate text-xs font-semibold text-foreground">
                      {finding.address}
                    </div>
                    <div className="mt-1 flex flex-wrap gap-2 text-[10px] font-mono uppercase tracking-widest text-muted-foreground">
                      <span>{finding.status}</span>
                      {finding.path && <span>{finding.path}</span>}
                    </div>
                  </div>
                  <span className={`flex-shrink-0 rounded border px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-widest ${classificationStyle(finding.classification)}`}>
                    {formatLabel(finding.classification)}
                  </span>
                </div>

                <p className="mt-3 text-xs leading-5 text-muted-foreground">{finding.reason}</p>

                <div className="mt-3 rounded border border-border bg-background/60 px-2 py-2">
                  <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                    Recommended action
                  </div>
                  <div className="mt-1 text-xs text-foreground">{formatLabel(finding.recommended_action)}</div>
                </div>

                {finding.path && (
                  <div className="mt-3 grid gap-2 text-xs">
                    <div className="rounded border border-border bg-background/60 px-2 py-2">
                      <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                        Code
                      </div>
                      <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-[11px] text-foreground">
                        {formatValue(finding.expected_value)}
                      </pre>
                    </div>
                    <div className="rounded border border-border bg-background/60 px-2 py-2">
                      <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                        State
                      </div>
                      <pre className="mt-1 whitespace-pre-wrap break-words font-mono text-[11px] text-foreground">
                        {formatValue(finding.current_value)}
                      </pre>
                    </div>
                  </div>
                )}
              </article>
            ))}
          </div>
        )}

        {suppressedFindings.length > 0 && (
          <section className="mt-4">
            <div className="mb-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
              Suppressed
            </div>
            <div className="space-y-3">
              {suppressedFindings.map((finding, index) => (
                <article
                  key={`suppressed-${finding.address}-${finding.path ?? finding.status}-${index}`}
                  className="rounded-md border border-border bg-card/50 p-3 opacity-75"
                >
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <div className="truncate text-xs font-semibold text-foreground">
                        {finding.address}
                      </div>
                      <div className="mt-1 flex flex-wrap gap-2 text-[10px] font-mono uppercase tracking-widest text-muted-foreground">
                        <span>{finding.status}</span>
                        {finding.path && <span>{finding.path}</span>}
                      </div>
                    </div>
                    <span className={`flex-shrink-0 rounded border px-2 py-0.5 font-mono text-[10px] font-bold uppercase tracking-widest ${classificationStyle(finding.classification)}`}>
                      {formatLabel(finding.classification)}
                    </span>
                  </div>

                  <div className="mt-3 rounded border border-border bg-background/50 px-2 py-2">
                    <div className="text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
                      Suppression reason
                    </div>
                    <div className="mt-1 text-xs text-foreground">
                      {finding.suppression_reason || 'suppressed by drift rule'}
                    </div>
                  </div>
                </article>
              ))}
            </div>
          </section>
        )}
      </div>
    </div>
  );
}
