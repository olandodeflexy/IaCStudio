import { type FormEvent, useRef, useState } from 'react';
import { AlertCircle, CheckCircle2, Clock3, Route, ShieldCheck, XCircle } from 'lucide-react';

import {
  api,
  type AgentToolRouteDecision,
  type AgentToolRoutePreviewInput,
  type MCPAirlockToolRisk,
} from '../../api';
import { Button } from '../ui/button';
import { Input } from '../ui/input';

type ToolRoutePreviewClient = Pick<typeof api, 'previewAgentToolRoute'>;

export interface ToolRoutePreviewPanelProps {
  projectName: string;
  runId: string;
  client?: ToolRoutePreviewClient;
}

const riskOptions: { value: MCPAirlockToolRisk; label: string }[] = [
  { value: 'read_only', label: 'Read only' },
  { value: 'generate_code', label: 'Generate code' },
  { value: 'modify_workspace', label: 'Modify workspace' },
  { value: 'cloud_mutation', label: 'Cloud mutation' },
  { value: 'secret_sensitive', label: 'Secret sensitive' },
  { value: 'destructive', label: 'Destructive' },
  { value: 'unknown', label: 'Unknown' },
];

const emptyInput: AgentToolRoutePreviewInput = {
  connection_id: '',
  server_id: '',
  tool_name: '',
  risk: 'read_only',
};

function normalizeInput(input: AgentToolRoutePreviewInput): AgentToolRoutePreviewInput {
  return {
    ...input,
    connection_id: input.connection_id.trim(),
    server_id: input.server_id.trim(),
    tool_name: input.tool_name.trim(),
  };
}

function validDecision(decision: AgentToolRouteDecision, risk: MCPAirlockToolRisk): boolean {
  if (!decision.untrusted_output) return false;
  switch (decision.status) {
    case 'allowed':
      return risk === 'read_only'
        && decision.allowed && !decision.approval_required && decision.reason === 'allowed';
    case 'approval_required':
      return !decision.allowed && decision.approval_required && decision.reason === 'approval_required';
    case 'denied':
      return !decision.allowed && !decision.approval_required
        && decision.reason !== 'allowed' && decision.reason !== 'approval_required';
    default:
      return false;
  }
}

function decisionLabel(status: AgentToolRouteDecision['status']): string {
  if (status === 'approval_required') return 'Approval required';
  return status === 'allowed' ? 'Allowed' : 'Denied';
}

function decisionStyle(status: AgentToolRouteDecision['status']): string {
  if (status === 'allowed') return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
  if (status === 'approval_required') return 'border-yellow-500/30 bg-yellow-500/10 text-yellow-200';
  return 'border-destructive/40 bg-destructive/10 text-destructive';
}

function DecisionIcon({ status }: { status: AgentToolRouteDecision['status'] }) {
  if (status === 'allowed') return <CheckCircle2 className="h-4 w-4" />;
  if (status === 'approval_required') return <Clock3 className="h-4 w-4" />;
  return <XCircle className="h-4 w-4" />;
}

export function ToolRoutePreviewPanel({
  projectName,
  runId,
  client = api,
}: ToolRoutePreviewPanelProps) {
  const [input, setInput] = useState<AgentToolRoutePreviewInput>(emptyInput);
  const [result, setResult] = useState<{ scope: string; decision: AgentToolRouteDecision } | null>(null);
  const [error, setError] = useState<{ scope: string; message: string } | null>(null);
  const [pending, setPending] = useState<{ id: number; scope: string } | null>(null);
  const requestSequence = useRef(0);
  const scope = JSON.stringify([projectName, runId]);
  const currentScope = useRef(scope);
  currentScope.current = scope;

  const normalized = normalizeInput(input);
  const ready = Boolean(
    projectName.trim()
      && runId.trim()
      && normalized.connection_id
      && normalized.server_id
      && normalized.tool_name,
  );
  const loading = pending?.scope === scope;
  const decision = result?.scope === scope ? result.decision : null;
  const errorMessage = error?.scope === scope ? error.message : null;

  function updateInput<K extends keyof AgentToolRoutePreviewInput>(
    field: K,
    value: AgentToolRoutePreviewInput[K],
  ) {
    requestSequence.current += 1;
    setPending(null);
    setResult(null);
    setError(null);
    setInput(current => ({ ...current, [field]: value }));
  }

  async function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || loading) return;

    const requestId = ++requestSequence.current;
    const requestScope = scope;
    setPending({ id: requestId, scope: requestScope });
    setResult(null);
    setError(null);
    try {
      const response = await client.previewAgentToolRoute(projectName, runId, normalized);
      if (requestSequence.current !== requestId || currentScope.current !== requestScope) return;
      if (!validDecision(response.decision, normalized.risk)) {
        setError({ scope: requestScope, message: 'Route preview returned an invalid decision.' });
        return;
      }
      setResult({ scope: requestScope, decision: response.decision });
    } catch (previewError) {
      if (requestSequence.current !== requestId || currentScope.current !== requestScope) return;
      setError({
        scope: requestScope,
        message: previewError instanceof Error ? previewError.message : 'Route preview failed.',
      });
    } finally {
      setPending(current => current?.id === requestId ? null : current);
    }
  }

  return (
    <section className="flex flex-col gap-3 bg-background p-4" aria-label="Tool route preview">
      <header className="flex items-center gap-3">
        <Route className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          Tool route
        </h2>
        <span className="ml-auto max-w-40 truncate font-mono text-[10px] text-muted-foreground" title={runId}>
          {runId}
        </span>
      </header>

      <form className="flex flex-col gap-3" onSubmit={preview} aria-busy={loading}>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex min-w-0 flex-col gap-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            Connection
            <Input
              value={input.connection_id}
              onChange={event => updateInput('connection_id', event.target.value)}
              placeholder="aws-prod"
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs normal-case"
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            MCP server
            <Input
              value={input.server_id}
              onChange={event => updateInput('server_id', event.target.value)}
              placeholder="aws-official"
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs normal-case"
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            Tool
            <Input
              value={input.tool_name}
              onChange={event => updateInput('tool_name', event.target.value)}
              placeholder="list_resources"
              autoComplete="off"
              spellCheck={false}
              className="font-mono text-xs normal-case"
            />
          </label>
          <label className="flex min-w-0 flex-col gap-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            Risk
            <select
              value={input.risk}
              onChange={event => updateInput('risk', event.target.value as MCPAirlockToolRisk)}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-xs font-medium normal-case text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {riskOptions.map(option => (
                <option key={option.value} value={option.value}>{option.label}</option>
              ))}
            </select>
          </label>
        </div>

        <Button type="submit" size="sm" className="self-end" disabled={!ready || loading}>
          <ShieldCheck className="h-3.5 w-3.5" />
          {loading ? 'Checking...' : 'Preview access'}
        </Button>
      </form>

      {errorMessage && (
        <div role="alert" className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{errorMessage}</span>
        </div>
      )}

      {decision && (
        <div aria-live="polite" className={`flex items-start gap-3 rounded-md border px-3 py-2 ${decisionStyle(decision.status)}`}>
          <DecisionIcon status={decision.status} />
          <div className="min-w-0 flex-1">
            <div className="text-xs font-semibold">{decisionLabel(decision.status)}</div>
            <div className="mt-0.5 font-mono text-[10px] opacity-80">
              {decision.reason.replaceAll('_', ' ')}
            </div>
          </div>
          <span className="shrink-0 font-mono text-[10px] uppercase tracking-widest opacity-80">
            Untrusted output
          </span>
        </div>
      )}
    </section>
  );
}
