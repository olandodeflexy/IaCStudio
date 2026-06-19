import { useEffect, useState } from 'react';
import { CheckCircle2, ExternalLink, Play, RefreshCw, ServerCog, ShieldCheck, Square, XCircle } from 'lucide-react';

import { api, type MCPAirlockServerStatus, type MCPAirlockToolInventory, type MCPAirlockToolRisk } from '../../api';
import { Button } from '../ui/button';

type MCPAirlockClient = Pick<typeof api, 'listMCPAirlockServers' | 'checkMCPAirlockServer' | 'startMCPAirlockServer' | 'stopMCPAirlockServer' | 'getMCPAirlockTools' | 'discoverMCPAirlockTools'>;

export interface MCPAirlockPanelProps {
  client?: MCPAirlockClient;
}

const stateLabels: Record<string, string> = {
  ready: 'Ready',
  running: 'Running',
  available: 'Available',
  not_configured: 'Not configured',
  command_missing: 'Missing',
  invalid_config: 'Invalid',
  unhealthy: 'Unhealthy',
  timeout: 'Timeout',
  blocked: 'Blocked',
};

function stateClass(state: string) {
  if (state === 'running') return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
  if (state === 'ready') return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
  if (state === 'available') return 'border-primary/30 bg-primary/10 text-primary';
  if (state === 'not_configured' || state === 'command_missing') return 'border-yellow-500/30 bg-yellow-500/10 text-yellow-200';
  return 'border-destructive/40 bg-destructive/10 text-destructive';
}

function checkIcon(status: string) {
  if (status === 'pass') return <CheckCircle2 className="h-3.5 w-3.5 text-emerald-300" />;
  if (status === 'warn') return <ShieldCheck className="h-3.5 w-3.5 text-yellow-200" />;
  return <XCircle className="h-3.5 w-3.5 text-destructive" />;
}

function riskClass(risk: MCPAirlockToolRisk) {
  if (risk === 'read_only') return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-300';
  if (risk === 'generate_code') return 'border-primary/30 bg-primary/10 text-primary';
  if (risk === 'modify_workspace') return 'border-yellow-500/30 bg-yellow-500/10 text-yellow-200';
  if (risk === 'cloud_mutation' || risk === 'secret_sensitive') return 'border-orange-500/30 bg-orange-500/10 text-orange-200';
  return 'border-destructive/40 bg-destructive/10 text-destructive';
}

function decisionClass(status: string) {
  if (status === 'allowed') return 'text-emerald-300';
  if (status === 'approval_required') return 'text-yellow-200';
  return 'text-destructive';
}

function canDiscoverTools(state: string) {
  return state === 'available' || state === 'ready' || state === 'running';
}

export function MCPAirlockPanel({ client = api }: MCPAirlockPanelProps) {
  const [servers, setServers] = useState<MCPAirlockServerStatus[]>([]);
  const [toolsByServer, setToolsByServer] = useState<Record<string, MCPAirlockToolInventory>>({});
  const [loading, setLoading] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [discoveringId, setDiscoveringId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = async () => {
    setLoading(true);
    setError(null);
    try {
      const nextServers = await client.listMCPAirlockServers();
      setServers(nextServers);
      const inventories = await Promise.all(
        nextServers.map(async status => {
          try {
            return [status.server.id, await client.getMCPAirlockTools(status.server.id)] as const;
          } catch {
            return [status.server.id, null] as const;
          }
        }),
      );
      setToolsByServer(Object.fromEntries(inventories.filter((entry): entry is readonly [string, MCPAirlockToolInventory] => entry[1] !== null)));
    } catch (err) {
      setError(String(err));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load();
  }, []);

  const check = async (id: string) => {
    setBusyId(id);
    setError(null);
    try {
      const next = await client.checkMCPAirlockServer(id);
      setServers(current => current.map(status => status.server.id === id ? next : status));
    } catch (err) {
      setError(String(err));
    } finally {
      setBusyId(null);
    }
  };

  const start = async (id: string) => {
    setBusyId(id);
    setError(null);
    try {
      const next = await client.startMCPAirlockServer(id);
      setServers(current => current.map(status => status.server.id === id ? next : status));
    } catch (err) {
      setError(String(err));
    } finally {
      setBusyId(null);
    }
  };

  const stop = async (id: string) => {
    setBusyId(id);
    setError(null);
    try {
      const next = await client.stopMCPAirlockServer(id);
      setServers(current => current.map(status => status.server.id === id ? next : status));
    } catch (err) {
      setError(String(err));
    } finally {
      setBusyId(null);
    }
  };

  const discover = async (id: string) => {
    setDiscoveringId(id);
    setError(null);
    try {
      const inventory = await client.discoverMCPAirlockTools(id);
      setToolsByServer(current => ({ ...current, [id]: inventory }));
    } catch (err) {
      setError(String(err));
    } finally {
      setDiscoveringId(null);
    }
  };

  return (
    <div className="flex h-full flex-col gap-3 bg-background p-4">
      <header className="flex items-center gap-3">
        <ServerCog className="h-4 w-4 text-primary" />
        <h2 className="text-sm font-semibold uppercase tracking-widest text-foreground">
          MCP Airlock
        </h2>
        <Button
          size="sm"
          variant="outline"
          className="ml-auto h-8 w-8 p-0"
          onClick={load}
          disabled={loading}
          title="Refresh MCP Airlock servers"
          aria-label="Refresh MCP Airlock servers"
        >
          <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
        </Button>
      </header>

      <div className="rounded-md border border-primary/30 bg-primary/10 px-3 py-2 text-xs leading-relaxed text-foreground">
        Trusted MCP servers run through read-only checks with cloud credentials withheld.
      </div>

      {error && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-xs text-destructive">
          {error}
        </div>
      )}

      <div className="flex-1 overflow-y-auto">
        {loading && servers.length === 0 && (
          <div className="p-6 text-center text-xs text-muted-foreground">Loading servers...</div>
        )}

        <ul className="flex flex-col gap-3">
          {servers.map(status => (
            <li
              key={status.server.id}
              className="rounded-md border border-border bg-card p-3"
            >
              {(() => {
                const busy = busyId === status.server.id;
                const discovering = discoveringId === status.server.id;
                const checkDisabled = busy || discovering;
                const startDisabled = busy || discovering || status.running || !status.command_available;
                const stopDisabled = busy || discovering || !status.running;
                const discoverDisabled = busy || discovering || !status.command_available || !canDiscoverTools(status.state);
                return (
              <div className="flex items-start gap-2">
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <h3 className="truncate text-sm font-semibold text-foreground">
                      {status.server.name}
                    </h3>
                    <span className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold ${stateClass(status.state)}`}>
                      {stateLabels[status.state] || status.state}
                    </span>
                  </div>
                  <div className="mt-0.5 text-[10px] uppercase tracking-widest text-muted-foreground">
                    {status.server.vendor} - {status.server.transport}
                  </div>
                </div>
                <div className="flex shrink-0 gap-1">
                  <Button
                    size="sm"
                    variant="secondary"
                    className="h-7 px-2 text-[10px]"
                    onClick={() => check(status.server.id)}
                    disabled={checkDisabled}
                  >
                    {busy ? '...' : 'Check'}
                  </Button>
                  <Button
                    size="sm"
                    variant="secondary"
                    className="h-7 px-2 text-[10px]"
                    onClick={() => discover(status.server.id)}
                    disabled={discoverDisabled}
                  >
                    {discovering ? '...' : 'Tools'}
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-7 w-7 p-0"
                    title="Start MCP server"
                    aria-label={`Start ${status.server.name}`}
                    onClick={() => start(status.server.id)}
                    disabled={startDisabled}
                  >
                    <Play className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    size="sm"
                    variant="outline"
                    className="h-7 w-7 p-0"
                    title="Stop MCP server"
                    aria-label={`Stop ${status.server.name}`}
                    onClick={() => stop(status.server.id)}
                    disabled={stopDisabled}
                  >
                    <Square className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
                );
              })()}

              <p className="mt-2 text-xs leading-relaxed text-muted-foreground">
                {status.summary}
              </p>

              <div className="mt-2 flex flex-wrap gap-1.5">
                {status.server.trusted && (
                  <span className="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[10px] text-emerald-300">trusted</span>
                )}
                {status.server.read_only_default && (
                  <span className="rounded bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary">read-only</span>
                )}
                <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                  credentials: {status.server.credential_mode}
                </span>
              </div>

              {toolsByServer[status.server.id] && (
                <div className="mt-3 rounded-md border border-border bg-background/70 p-2">
                  <div className="flex items-center gap-2 text-[10px] uppercase tracking-widest text-muted-foreground">
                    <ShieldCheck className="h-3.5 w-3.5 text-primary" />
                    Tool Firewall
                    <span className="ml-auto normal-case tracking-normal">
                      {toolsByServer[status.server.id].tools.length} tools
                    </span>
                  </div>

                  {toolsByServer[status.server.id].checks.length > 0 && (
                    <div className="mt-2 grid gap-1">
                      {toolsByServer[status.server.id].checks.slice(0, 2).map(check => (
                        <div key={`${status.server.id}-tools-${check.name}`} className="flex items-start gap-1.5 text-[10px] leading-relaxed text-muted-foreground">
                          {checkIcon(check.status)}
                          <span>{check.message}</span>
                        </div>
                      ))}
                    </div>
                  )}

                  {toolsByServer[status.server.id].tools.length > 0 && (
                    <div className="mt-2 grid gap-1.5">
                      {toolsByServer[status.server.id].tools.slice(0, 5).map(tool => (
                        <div key={`${status.server.id}-${tool.name}`} className="rounded border border-border/80 px-2 py-1.5">
                          <div className="flex items-center gap-1.5">
                            <span className="min-w-0 flex-1 truncate text-[11px] font-medium text-foreground">
                              {tool.name}
                            </span>
                            <span className={`rounded border px-1.5 py-0.5 text-[9px] ${riskClass(tool.risk)}`}>
                              {tool.risk.replace('_', ' ')}
                            </span>
                          </div>
                          <div className="mt-1 flex items-center gap-2 text-[10px] text-muted-foreground">
                            <span className={decisionClass(tool.decision.status)}>
                              {tool.decision.status.replace('_', ' ')}
                            </span>
                            <span>{tool.schema_state}</span>
                          </div>
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              )}

              {status.server.install_hint && !status.command_available && (
                <div className="mt-2 rounded-md border border-border bg-background px-2 py-1.5 text-[11px] leading-relaxed text-muted-foreground">
                  {status.server.install_hint}
                </div>
              )}

              {status.server.capabilities && status.server.capabilities.length > 0 && (
                <div className="mt-2 flex flex-wrap gap-1">
                  {status.server.capabilities.slice(0, 4).map(capability => (
                    <span key={capability} className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">
                      {capability}
                    </span>
                  ))}
                </div>
              )}

              <div className="mt-3 grid gap-1.5">
                {status.checks.map(check => (
                  <div key={`${status.server.id}-${check.name}`} className="flex items-start gap-2 text-[11px] leading-relaxed text-muted-foreground">
                    {checkIcon(check.status)}
                    <span className="min-w-0">
                      <span className="font-medium text-foreground">{check.name}</span>: {check.message}
                    </span>
                  </div>
                ))}
              </div>

              <div className="mt-3 flex items-center gap-3 text-[10px] text-muted-foreground">
                {status.started_at && <span>started {new Date(status.started_at).toLocaleTimeString()}</span>}
                {status.last_exit_at && !status.running && <span>last exit {new Date(status.last_exit_at).toLocaleTimeString()}</span>}
                {status.checked_at && <span>checked {new Date(status.checked_at).toLocaleTimeString()}</span>}
                <a
                  className="ml-auto inline-flex items-center gap-1 hover:text-foreground"
                  href={status.server.docs_url || status.server.source_url}
                  target="_blank"
                  rel="noreferrer"
                >
                  docs <ExternalLink className="h-3 w-3" />
                </a>
              </div>
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}
