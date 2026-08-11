import { type FormEvent, useRef, useState } from 'react';
import { AlertCircle, CheckCircle2, Clock3, Play, RotateCcw, Route, ShieldCheck, XCircle } from 'lucide-react';

import {
  api,
  type AgentRunApproval,
  type AgentToolCallResult,
  type AgentToolExecutionResponse,
  type AgentToolRouteDecision,
  type AgentToolRoutePreviewInput,
  type MCPAirlockToolRisk,
} from '../../api';
import { Button } from '../ui/button';
import { Input } from '../ui/input';

type ToolRoutePreviewClient = Pick<typeof api, 'previewAgentToolRoute'>;
type ToolRouteExecutionClient = Pick<typeof api, 'executeAgentToolRoute'>;

export interface ToolRoutePreviewPanelProps {
  projectName: string;
  runId: string;
  client?: ToolRoutePreviewClient;
  executionClient?: ToolRouteExecutionClient;
  idempotencyKeyFactory?: () => string;
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

const deniedReasons = new Set<AgentToolRouteDecision['reason']>([
  'invalid_request',
  'invalid_policy',
  'policy_unavailable',
  'mode_risk_mismatch',
  'no_matching_rule',
  'policy_denied',
  'airlock_unavailable',
  'airlock_server_mismatch',
  'airlock_tool_mismatch',
  'airlock_risk_mismatch',
  'invalid_airlock_decision',
  'airlock_blocked',
]);

const approvalKinds = new Set<AgentRunApproval['kind']>([
  'file_write',
  'command',
  'iac_action',
  'cloud_write',
  'secret_read',
  'mcp_network',
]);

const approvalStatuses = new Set<AgentRunApproval['status']>(['pending', 'approved', 'rejected']);

function isRecord(value: unknown): value is Record<string, unknown> {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function validApproval(value: unknown): value is AgentRunApproval {
  return isRecord(value)
    && typeof value.id === 'string'
    && value.id.length > 0
    && typeof value.kind === 'string'
    && approvalKinds.has(value.kind as AgentRunApproval['kind'])
    && typeof value.status === 'string'
    && approvalStatuses.has(value.status as AgentRunApproval['status'])
    && typeof value.summary === 'string'
    && value.summary.length > 0
    && typeof value.created_at === 'string'
    && value.created_at.length > 0;
}

function validDecision(
  decision: unknown,
  risk: MCPAirlockToolRisk,
): decision is AgentToolRouteDecision {
  if (!isRecord(decision)
    || decision.untrusted_output !== true
    || typeof decision.allowed !== 'boolean'
    || typeof decision.approval_required !== 'boolean') {
    return false;
  }
  switch (decision.status) {
    case 'allowed':
      return risk === 'read_only'
        && decision.allowed && !decision.approval_required && decision.reason === 'allowed';
    case 'approval_required':
      return !decision.allowed && decision.approval_required && decision.reason === 'approval_required';
    case 'denied':
      return !decision.allowed && !decision.approval_required
        && deniedReasons.has(decision.reason as AgentToolRouteDecision['reason']);
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

function parseArguments(value: string): Record<string, unknown> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(value);
  } catch {
    throw new Error('Arguments must be valid JSON.');
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('Arguments must be a JSON object.');
  }
  return parsed as Record<string, unknown>;
}

function validExecutionResponse(
  response: unknown,
  projectName: string,
  runId: string,
): response is AgentToolExecutionResponse & { result: AgentToolCallResult } {
  if (!isRecord(response)
    || response.invoked !== true
    || !isRecord(response.route)
    || !isRecord(response.route.run)
    || !isRecord(response.result)) {
    return false;
  }
  const { result, route } = response;
  const run = route.run;
  const decision = route.decision;
  return result.untrusted_output === true
    && typeof result.output === 'string'
    && typeof result.is_error === 'boolean'
    && typeof result.redacted === 'boolean'
    && typeof result.truncated === 'boolean'
    && validDecision(decision, 'read_only')
    && decision.status === 'allowed'
    && run.id === runId
    && run.project === projectName
    && run.mode === 'read_only'
    && run.canceled === false;
}

function pendingApprovalFromExecutionResponse(
  response: unknown,
  projectName: string,
  runId: string,
): AgentRunApproval | null {
  if (!isRecord(response)
    || response.invoked !== false
    || response.result !== undefined
    || !isRecord(response.route)
    || !isRecord(response.route.run)) {
    return null;
  }
  const { route } = response;
  const run = route.run;
  if (!validDecision(route.decision, 'unknown')
    || route.decision.status !== 'approval_required'
    || run.id !== runId
    || run.project !== projectName
    || run.mode !== 'approved_execute'
    || run.status !== 'waiting_approval'
    || run.canceled !== false
    || !Array.isArray(run.approvals)) {
    return null;
  }
  if (!run.approvals.every(validApproval)) return null;
  const pending = run.approvals.filter(approval => approval.status === 'pending');
  return pending.length === 1 ? pending[0] : null;
}

function createIdempotencyKey(): string {
  if (!globalThis.crypto?.randomUUID) {
    throw new Error('Secure execution identity is unavailable in this browser.');
  }
  return globalThis.crypto.randomUUID();
}

export function ToolRoutePreviewPanel({
  projectName,
  runId,
  client = api,
  executionClient = api,
  idempotencyKeyFactory = createIdempotencyKey,
}: ToolRoutePreviewPanelProps) {
  const [input, setInput] = useState<AgentToolRoutePreviewInput>(emptyInput);
  const [result, setResult] = useState<{ scope: string; decision: AgentToolRouteDecision } | null>(null);
  const [error, setError] = useState<{ scope: string; message: string } | null>(null);
  const [pending, setPending] = useState<{ id: number; scope: string } | null>(null);
  const [argumentsText, setArgumentsText] = useState('{}');
  const [execution, setExecution] = useState<{ scope: string; result: AgentToolCallResult } | null>(null);
  const [approvalRequest, setApprovalRequest] = useState<{ scope: string; gate: AgentRunApproval } | null>(null);
  const [executionError, setExecutionError] = useState<{ scope: string; message: string } | null>(null);
  const [executionPending, setExecutionPending] = useState<{ id: number; scope: string } | null>(null);
  const requestSequence = useRef(0);
  const executionSequence = useRef(0);
  const executionIdentity = useRef<{ scope: string; key: string } | null>(null);
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
  const executionResult = execution?.scope === scope ? execution.result : null;
  const approvalGate = approvalRequest?.scope === scope ? approvalRequest.gate : null;
  const executionErrorMessage = executionError?.scope === scope ? executionError.message : null;
  const executing = executionPending?.scope === scope;
  const approvalRequired = decision?.status === 'approval_required';
  let executionButtonLabel = 'Execute read-only';
  if (executing) executionButtonLabel = approvalRequired ? 'Requesting...' : 'Executing...';
  else if (approvalGate) executionButtonLabel = 'Waiting for approval';
  else if (approvalRequired) executionButtonLabel = executionErrorMessage ? 'Retry approval request' : 'Request approval';
  else if (executionResult) executionButtonLabel = 'Replay result';
  else if (executionErrorMessage) executionButtonLabel = 'Retry execution';

  function clearExecution() {
    executionSequence.current += 1;
    executionIdentity.current = null;
    setExecutionPending(null);
    setExecution(null);
    setApprovalRequest(null);
    setExecutionError(null);
  }

  function updateInput<K extends keyof AgentToolRoutePreviewInput>(
    field: K,
    value: AgentToolRoutePreviewInput[K],
  ) {
    requestSequence.current += 1;
    setPending(null);
    setResult(null);
    setError(null);
    clearExecution();
    setInput(current => ({ ...current, [field]: value }));
  }

  function updateArguments(value: string) {
    setArgumentsText(value);
    clearExecution();
  }

  async function preview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!ready || loading) return;

    const requestId = ++requestSequence.current;
    const requestScope = scope;
    setPending({ id: requestId, scope: requestScope });
    setResult(null);
    setError(null);
    clearExecution();
    try {
      const response = await client.previewAgentToolRoute(projectName, runId, normalized);
      if (requestSequence.current !== requestId || currentScope.current !== requestScope) return;
      if (!validDecision(response?.decision, normalized.risk)) {
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

  async function execute(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if ((decision?.status !== 'allowed' && decision?.status !== 'approval_required') || executing || approvalGate) return;

    let argumentsObject: Record<string, unknown>;
    try {
      argumentsObject = parseArguments(argumentsText);
    } catch (argumentError) {
      setExecutionError({
        scope,
        message: argumentError instanceof Error ? argumentError.message : 'Arguments are invalid.',
      });
      return;
    }

    let idempotencyKey = executionIdentity.current?.scope === scope
      ? executionIdentity.current.key
      : null;
    try {
      if (!idempotencyKey) {
        idempotencyKey = idempotencyKeyFactory();
        executionIdentity.current = { scope, key: idempotencyKey };
      }
    } catch (keyError) {
      setExecutionError({
        scope,
        message: keyError instanceof Error ? keyError.message : 'Could not create execution identity.',
      });
      return;
    }

    const requestId = ++executionSequence.current;
    const requestScope = scope;
    setExecutionPending({ id: requestId, scope: requestScope });
    setExecution(null);
    setExecutionError(null);
    try {
      const response = await executionClient.executeAgentToolRoute(
        projectName,
        runId,
        {
          connection_id: normalized.connection_id,
          server_id: normalized.server_id,
          tool_name: normalized.tool_name,
          arguments: argumentsObject,
        },
        idempotencyKey,
      );
      if (executionSequence.current !== requestId || currentScope.current !== requestScope) return;
      if (decision.status === 'approval_required') {
        const gate = pendingApprovalFromExecutionResponse(response, projectName, runId);
        if (!gate) {
          setExecutionError({ scope: requestScope, message: 'Tool execution returned an invalid response.' });
          return;
        }
        setApprovalRequest({ scope: requestScope, gate });
        return;
      }
      if (!validExecutionResponse(response, projectName, runId)) {
        setExecutionError({ scope: requestScope, message: 'Tool execution returned an invalid response.' });
        return;
      }
      setExecution({ scope: requestScope, result: response.result });
    } catch (executionFailure) {
      if (executionSequence.current !== requestId || currentScope.current !== requestScope) return;
      setExecutionError({
        scope: requestScope,
        message: executionFailure instanceof Error ? executionFailure.message : 'Tool execution failed.',
      });
    } finally {
      setExecutionPending(current => current?.id === requestId ? null : current);
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
              disabled={Boolean(approvalGate)}
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
              disabled={Boolean(approvalGate)}
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
              disabled={Boolean(approvalGate)}
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
              disabled={Boolean(approvalGate)}
              className="h-9 w-full rounded-md border border-input bg-background px-3 text-xs font-medium normal-case text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              {riskOptions.map(option => (
                <option key={option.value} value={option.value}>{option.label}</option>
              ))}
            </select>
          </label>
        </div>

        <Button type="submit" size="sm" className="self-end" disabled={!ready || loading || Boolean(approvalGate)}>
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

      {(decision?.status === 'allowed' || decision?.status === 'approval_required') && (
        <form className="flex flex-col gap-3 border-t border-border pt-3" onSubmit={execute} aria-busy={executing}>
          <label className="flex min-w-0 flex-col gap-1 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            Arguments (JSON)
            <textarea
              value={argumentsText}
              onChange={event => updateArguments(event.target.value)}
              disabled={executing || Boolean(approvalGate)}
              rows={4}
              spellCheck={false}
              className="w-full resize-y rounded-md border border-input bg-background px-3 py-2 font-mono text-xs font-normal normal-case text-foreground shadow-sm focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          </label>

          {executionErrorMessage && (
            <div role="alert" className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <span>{executionErrorMessage}</span>
            </div>
          )}

          {approvalGate && (
            <div aria-label="MCP approval request" aria-live="polite" className="flex items-start gap-2 rounded-md border border-yellow-500/30 bg-yellow-500/10 px-3 py-2 text-xs text-yellow-200">
              <Clock3 className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              <div className="min-w-0 flex-1">
                <div className="font-semibold">Approval requested</div>
                <div className="mt-0.5 break-words">{approvalGate.summary}</div>
                <div className="mt-1 flex flex-wrap gap-2 font-mono text-[10px] uppercase tracking-widest opacity-80">
                  <span>{approvalGate.kind.replaceAll('_', ' ')}</span>
                  <span>{approvalGate.id}</span>
                </div>
              </div>
            </div>
          )}

          <div className="flex justify-end gap-2">
            {executionResult && (
              <Button type="button" size="sm" variant="outline" onClick={clearExecution} disabled={executing}>
                <RotateCcw className="h-3.5 w-3.5" />
                New execution
              </Button>
            )}
            <Button type="submit" size="sm" disabled={executing || Boolean(approvalGate)}>
              {approvalRequired ? <Clock3 className="h-3.5 w-3.5" /> : <Play className="h-3.5 w-3.5" />}
              {executionButtonLabel}
            </Button>
          </div>
        </form>
      )}

      {executionResult && (
        <div aria-label="Untrusted MCP output" aria-live="polite" className="border-t border-border pt-3">
          <div className="mb-2 flex flex-wrap items-center gap-2 text-[10px] font-semibold uppercase tracking-widest text-muted-foreground">
            <span>Untrusted MCP output</span>
            {executionResult.is_error && <span className="text-destructive">Tool error</span>}
            {executionResult.redacted && <span className="text-yellow-300">Redacted</span>}
            {executionResult.truncated && <span className="text-yellow-300">Truncated</span>}
          </div>
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-md border border-border bg-muted/20 p-3 font-mono text-xs text-foreground">
            {executionResult.output || 'No output returned.'}
          </pre>
        </div>
      )}
    </section>
  );
}
