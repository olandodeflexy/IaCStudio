import { useCallback, useRef, type Dispatch, type MouseEvent, type MutableRefObject, type RefObject, type SetStateAction } from 'react';
import { api, ApiError, normalizeSuggestions, type CatalogResource, type CloudConnection, type PlanClassification, type PolicyFinding, type Suggestion } from '../api';
import {
  TOOLS,
  edgeId,
  uid,
  type Edge,
} from '../legacy';
import type { AppResource, ChatMessage, HistorySetter } from './types';

const summarizePolicyFindings = (findings: PolicyFinding[]) => {
  if (findings.length === 0) return 'No finding details were returned.';
  const shown = findings.slice(0, 5).map((finding, index) => {
    const target = finding.resource ? ` on ${finding.resource}` : '';
    return `${index + 1}. [${finding.engine}] ${finding.policy_id}${target}: ${finding.message}`;
  });
  if (findings.length > shown.length) {
    shown.push(`...and ${findings.length - shown.length} more.`);
  }
  return shown.join('\n');
};

const isPolicyBlockedError = (err: unknown): err is ApiError => (
  err instanceof ApiError && err.status === 409 && err.payload?.error === 'policy_blocked'
);

const summarizePlanClassification = (classification?: PlanClassification) => {
  if (!classification) return 'No classifier details were returned.';
  const lines = [classification.summary.text];
  const priority = { destructive: 4, risky: 3, unknown: 2, safe: 1 } as const;
  const changes = [...classification.changes]
    .sort((a, b) => priority[b.risk] - priority[a.risk])
    .slice(0, 5)
    .map((change, index) => {
      const focus = change.reviewer_focus?.length ? ` Focus: ${change.reviewer_focus.join(' ')}` : '';
      return `${index + 1}. [${change.risk}] ${change.action} ${change.address}: ${change.reason}${focus}`;
    });
  lines.push(...changes);
  if (classification.changes.length > changes.length) {
    lines.push(`...and ${classification.changes.length - changes.length} more.`);
  }
  return lines.join('\n');
};

const isPlanRiskBlockedError = (err: unknown): err is ApiError => (
  err instanceof ApiError && err.status === 409 && err.payload?.error === 'plan_risk_blocked'
);

export interface UseWorkspaceActionsInput {
  tool: string | null;
  concreteTool: string;
  projectId: string;
  activeEnv: string | null;
  selectedCloudConnection: CloudConnection | null;
  latestPlanHash?: string | null;
  activeResourceFile?: string;
  unresolvedHybridEnv: boolean;
  nodes: AppResource[];
  edges: Edge[];
  catalogResources: CatalogResource[];
  chatMessages: ChatMessage[];
  chatInput: string;
  chatLoading: boolean;
  connecting: { fromId: string; x: number; y: number } | null;
  dragging: { id: string; ox: number; oy: number } | null;
  lastCmdError: { command: string; output: string } | null;
  canvasRef: RefObject<HTMLElement>;
  setNodes: HistorySetter<AppResource[]>;
  setEdges: Dispatch<SetStateAction<Edge[]>>;
  setSelectedNode: Dispatch<SetStateAction<string | null>>;
  setSelectedEdge: Dispatch<SetStateAction<string | null>>;
  setConnecting: Dispatch<SetStateAction<{ fromId: string; x: number; y: number } | null>>;
  setDragging: Dispatch<SetStateAction<{ id: string; ox: number; oy: number } | null>>;
  setChatMessages: Dispatch<SetStateAction<ChatMessage[]>>;
  setChatInput: Dispatch<SetStateAction<string>>;
  setChatLoading: Dispatch<SetStateAction<boolean>>;
  setSuggestions: Dispatch<SetStateAction<Suggestion[]>>;
  setTerminalOutput: Dispatch<SetStateAction<string[]>>;
  setLastCmdError: Dispatch<SetStateAction<{ command: string; output: string } | null>>;
  setFixLoading: Dispatch<SetStateAction<boolean>>;
  showNotification: (_message: string, _duration?: number) => void;
}

export function useWorkspaceActions({
  tool,
  concreteTool,
  projectId,
  activeEnv,
  selectedCloudConnection,
  latestPlanHash,
  activeResourceFile,
  unresolvedHybridEnv,
  nodes,
  edges,
  catalogResources,
  chatMessages,
  chatInput,
  chatLoading,
  connecting,
  dragging,
  lastCmdError,
  canvasRef,
  setNodes,
  setEdges,
  setSelectedNode,
  setSelectedEdge,
  setConnecting,
  setDragging,
  setChatMessages,
  setChatInput,
  setChatLoading,
  setSuggestions,
  setTerminalOutput,
  setLastCmdError,
  setFixLoading,
  showNotification,
}: UseWorkspaceActionsInput) {
  const chatInFlightRef = useRef(false);

  const addNode = useCallback((resourceDef: any) => {
    if (unresolvedHybridEnv) {
      showNotification(`Environment "${activeEnv}" has no configured IaC tool`, 4000);
      return;
    }
    const node: AppResource = {
      id: uid(),
      type: resourceDef.type,
      name: resourceDef.type.replace(/^(aws_|google_|azurerm_)/, '').replace(/^compute_|^container_/, ''),
      label: resourceDef.label,
      icon: resourceDef.icon,
      properties: { ...(resourceDef.defaults || {}) },
      file: activeResourceFile,
      x: 100 + Math.random() * 280,
      y: 80 + Math.random() * 180,
    };
    setNodes(prev => {
      const catEntry = catalogResources.find(resource => resource.type === resourceDef.type);
      if (catEntry?.connects_via) {
        const newEdges: Edge[] = [];
        for (const [field, targetType] of Object.entries(catEntry.connects_via)) {
          const target = prev.find(resource => resource.type === targetType);
          if (target) {
            newEdges.push({
              id: edgeId(node.id, target.id, field),
              from: node.id,
              to: target.id,
              fromType: node.type,
              toType: target.type,
              field,
              label: field.replace(/_/g, ' '),
            });
          }
        }
        if (newEdges.length > 0) {
          setEdges(prevEdges => [...prevEdges, ...newEdges]);
        }
      }
      return [...prev, node];
    });
    setSelectedNode(node.id);
  }, [activeEnv, activeResourceFile, catalogResources, setEdges, setNodes, setSelectedNode, showNotification, unresolvedHybridEnv]);

  const removeNode = useCallback((id: string) => {
    setNodes(prev => prev.filter(node => node.id !== id));
    setEdges(prev => prev.filter(edge => edge.from !== id && edge.to !== id));
    setSelectedNode(prev => prev === id ? null : prev);
    setSelectedEdge(prev => {
      const edge = edges.find(candidate => candidate.id === prev);
      return edge && (edge.from === id || edge.to === id) ? null : prev;
    });
  }, [edges, setEdges, setNodes, setSelectedEdge, setSelectedNode]);

  const updateProp = useCallback((id: string, key: string, value: any) => {
    setNodes(prev => prev.map(node => node.id === id ? { ...node, properties: { ...node.properties, [key]: value } } : node));
  }, [setNodes]);

  const updateName = useCallback((id: string, name: string) => {
    setNodes(prev => prev.map(node => node.id === id ? { ...node, name } : node));
  }, [setNodes]);

  const onMouseDown = useCallback((event: MouseEvent, nodeId: string) => {
    event.stopPropagation();
    const rect = canvasRef.current!.getBoundingClientRect();
    const node = nodes.find(candidate => candidate.id === nodeId)!;
    setDragging({ id: nodeId, ox: event.clientX - rect.left - node.x, oy: event.clientY - rect.top - node.y });
    setSelectedNode(nodeId);
  }, [canvasRef, nodes, setDragging, setSelectedNode]);

  const onMouseMove = useCallback((event: MouseEvent) => {
    if (connecting) {
      const rect = canvasRef.current!.getBoundingClientRect();
      setConnecting(prev => prev ? { ...prev, x: event.clientX - rect.left, y: event.clientY - rect.top } : null);
    }
    if (!dragging) return;
    const rect = canvasRef.current!.getBoundingClientRect();
    const x = Math.max(0, event.clientX - rect.left - dragging.ox);
    const y = Math.max(0, event.clientY - rect.top - dragging.oy);
    setNodes(prev => prev.map(node => node.id === dragging.id ? { ...node, x, y } : node));
  }, [canvasRef, connecting, dragging, setConnecting, setNodes]);

  const onMouseUp = useCallback(() => setDragging(null), [setDragging]);

  const handleSelectNode = useCallback((id: string) => {
    setSelectedNode(id);
    setSelectedEdge(null);
  }, [setSelectedEdge, setSelectedNode]);

  const handleSelectEdge = useCallback((id: string) => {
    setSelectedEdge(id);
    setSelectedNode(null);
  }, [setSelectedEdge, setSelectedNode]);

  const handleClearCanvasSelection = useCallback(() => {
    setSelectedNode(null);
    setSelectedEdge(null);
  }, [setSelectedEdge, setSelectedNode]);

  const handleStartConnection = useCallback((nodeId: string, position: { x: number; y: number }) => {
    setConnecting({ fromId: nodeId, ...position });
  }, [setConnecting]);

  const handleCancelConnection = useCallback(() => {
    setConnecting(null);
  }, [setConnecting]);

  const handleCompleteConnection = useCallback((targetNodeId: string) => {
    if (!connecting) return;
    if (connecting.fromId === targetNodeId) {
      setConnecting(null);
      return;
    }
    const fromNode = nodes.find(node => node.id === connecting.fromId);
    const toNode = nodes.find(node => node.id === targetNodeId);
    if (!fromNode || !toNode) {
      setConnecting(null);
      return;
    }

    const catEntry = catalogResources.find(resource => resource.type === fromNode.type);
    let field = 'depends_on';
    if (catEntry?.connects_via) {
      const match = Object.entries(catEntry.connects_via).find(([, targetType]) => targetType === toNode.type);
      if (match) field = match[0];
    }
    const newEdge: Edge = {
      id: edgeId(connecting.fromId, targetNodeId, field),
      from: connecting.fromId,
      to: targetNodeId,
      fromType: fromNode.type,
      toType: toNode.type,
      field,
      label: field.replace(/_/g, ' '),
    };
    setEdges(prev => {
      if (prev.some(edge => edge.from === newEdge.from && edge.to === newEdge.to && edge.field === newEdge.field)) return prev;
      return [...prev, newEdge];
    });
    setConnecting(null);
  }, [catalogResources, connecting, nodes, setConnecting, setEdges]);

  const detectProvider = useCallback((): string => {
    const counts: Record<string, number> = { aws: 0, google: 0, azurerm: 0 };
    nodes.forEach(node => {
      if (node.type.startsWith('aws_')) counts.aws++;
      else if (node.type.startsWith('google_')) counts.google++;
      else if (node.type.startsWith('azurerm_')) counts.azurerm++;
    });
    const chatText = chatMessages.map(message => message.text).join(' ').toLowerCase();
    if (chatText.includes('azure') || chatText.includes('azurerm')) counts.azurerm += 3;
    if (chatText.includes('gcp') || chatText.includes('google cloud')) counts.google += 3;
    if (chatText.includes('aws') || chatText.includes('amazon')) counts.aws += 3;

    const max = Math.max(counts.aws, counts.google, counts.azurerm);
    if (max === 0) return 'aws';
    if (counts.google === max) return 'google';
    if (counts.azurerm === max) return 'azurerm';
    return 'aws';
  }, [chatMessages, nodes]);

  const handleChat = useCallback(async () => {
    if (chatLoading || chatInFlightRef.current) return;
    if (!chatInput.trim() || !tool) return;

    chatInFlightRef.current = true;
    setChatLoading(true);

    const input = chatInput;
    setChatInput('');
    const aiMessageId = `ai-${Date.now()}-${Math.random().toString(36).slice(2)}`;
    let pendingAiText = '';
    const updateAiMessageText = (text: string) => {
      setChatMessages(prev => {
        const nextAiIndex = prev.findIndex(message => message.id === aiMessageId);
        if (nextAiIndex < 0) return prev;
        const next = [...prev];
        next[nextAiIndex] = { ...next[nextAiIndex], text };
        return next;
      });
    };

    setChatMessages(prev => [
      ...prev,
      { role: 'user' as const, text: input },
      { role: 'ai' as const, text: pendingAiText, id: aiMessageId },
    ]);

    try {
      const provider = detectProvider();
      const history = chatMessages.map(message => ({ role: message.role === 'ai' ? 'ai' : 'user', content: message.text }));
      const canvas = nodes.map(node => ({ type: node.type, name: node.name }));

      const result = await api.chatStream(
        { message: input, tool, provider, history, canvas },
        (delta: string) => {
          pendingAiText += delta;
          updateAiMessageText(pendingAiText);
        },
      );

      pendingAiText = result.message;
      updateAiMessageText(pendingAiText);
      if (result.suggestions !== undefined) {
        setSuggestions(normalizeSuggestions(result.suggestions));
      }
      if (result.resources) {
        result.resources.forEach(resource => {
          const meta = catalogResources.find(definition => definition.type === resource.type);
          addNode({
            type: resource.type,
            label: meta?.label ?? resource.type,
            icon: meta?.icon ?? '📦',
            defaults: resource.properties,
          });
        });
      }
    } catch {
      pendingAiText = 'AI is unavailable. Make sure your provider is reachable.';
      updateAiMessageText(pendingAiText);
    } finally {
      chatInFlightRef.current = false;
      setChatLoading(false);
    }
  }, [addNode, catalogResources, chatInput, chatLoading, chatMessages, detectProvider, nodes, setChatInput, setChatLoading, setChatMessages, setSuggestions, tool]);

  const runCmd = useCallback((command: string) => {
    if (!tool) return;
    if (unresolvedHybridEnv) {
      setTerminalOutput(prev => [...prev, `Error: environment "${activeEnv}" has no configured IaC tool`]);
      return;
    }
    const needsApproval = command === 'apply' || command === 'destroy';
    if (needsApproval && !confirm(`Are you sure you want to run "${command}"? This will modify real infrastructure.`)) {
      return;
    }
    const connectionLabel = selectedCloudConnection ? ` --connection ${selectedCloudConnection.name}` : '';
    setTerminalOutput(prev => [...prev, `$ ${command}${connectionLabel}`, '']);

    const handlePolicyBlock = (err: ApiError, riskAcknowledged: boolean) => {
      const findings = err.payload?.findings ?? [];
      const blockingCount = findings.filter(finding => finding.severity === 'error').length;
      const summary = summarizePolicyFindings(findings);
      const summaryLines = summary.split('\n');
      setTerminalOutput(prev => [
        ...prev,
        `Policy blocked ${command}: ${blockingCount} blocking finding${blockingCount === 1 ? '' : 's'}`,
        ...summaryLines,
      ]);
      if (!confirm(`Policy checks blocked "${command}".\n\n${summary}\n\nRun it anyway and acknowledge these findings?`)) {
        return;
      }
      setTerminalOutput(prev => [...prev, `$ ${command} --acknowledged${connectionLabel}`, '']);
      api.runCommand(projectId, tool, command, {
        approved: true,
        env: activeEnv,
        acknowledged: true,
        riskAcknowledged,
        connectionId: selectedCloudConnection?.id,
        planHash: needsApproval ? latestPlanHash : undefined,
      }).catch(overrideErr => {
        setTerminalOutput(prev => [...prev, `Error: ${overrideErr.message}`]);
      });
    };

    api.runCommand(projectId, tool, command, {
      approved: needsApproval,
      env: activeEnv,
      connectionId: selectedCloudConnection?.id,
      planHash: needsApproval ? latestPlanHash : undefined,
    }).catch(err => {
      if (needsApproval && isPlanRiskBlockedError(err)) {
        const summary = summarizePlanClassification(err.payload?.classification);
        setTerminalOutput(prev => [
          ...prev,
          `Semantic plan gate blocked ${command}:`,
          ...summary.split('\n'),
        ]);
        if (!confirm(`Semantic plan review flagged "${command}".\n\n${summary}\n\nRun it anyway and acknowledge this risk?`)) {
          return;
        }
        setTerminalOutput(prev => [...prev, `$ ${command} --risk-acknowledged${connectionLabel}`, '']);
        api.runCommand(projectId, tool, command, {
          approved: true,
          env: activeEnv,
          riskAcknowledged: true,
          connectionId: selectedCloudConnection?.id,
          planHash: latestPlanHash,
        }).catch(overrideErr => {
          if (needsApproval && isPolicyBlockedError(overrideErr)) {
            handlePolicyBlock(overrideErr, true);
            return;
          }
          setTerminalOutput(prev => [...prev, `Error: ${overrideErr.message}`]);
        });
        return;
      }
      if (needsApproval && isPolicyBlockedError(err)) {
        handlePolicyBlock(err, false);
        return;
      }
      setTerminalOutput(prev => [...prev, `Error: ${err.message}`]);
    });
  }, [activeEnv, latestPlanHash, projectId, selectedCloudConnection, setTerminalOutput, tool, unresolvedHybridEnv]);

  const fixLastError = useCallback(async () => {
    if (!lastCmdError || !tool) return;
    setFixLoading(true);
    try {
      const provider = detectProvider();
      const result = await api.analyzePlan({
        tool,
        provider,
        command: lastCmdError.command,
        output: lastCmdError.output,
        exit_code: 1,
        canvas: nodes.map(node => ({ type: node.type, name: node.name })),
      });
      setTerminalOutput(prev => [...prev, '', `✦ AI Diagnosis: ${result.message}`]);
      if (result.fixes?.length > 0) {
        setTerminalOutput(prev => [...prev, '✦ Suggested fixes:']);
        result.fixes.forEach(fix => {
          setTerminalOutput(prev => [...prev, `  → ${fix.resource_type}.${fix.resource_name}: ${fix.field} = "${fix.new_value}" (${fix.reason})`]);
          setNodes(prev => prev.map(node => {
            if (node.type === fix.resource_type && node.name === fix.resource_name) {
              return { ...node, properties: { ...node.properties, [fix.field]: fix.new_value } };
            }
            return node;
          }));
        });
        setTerminalOutput(prev => [...prev, '✦ Fixes applied to canvas. Run plan again to verify.']);
      }
      if (result.new_resources?.length > 0) {
        setTerminalOutput(prev => [...prev, '✦ Adding missing resources:']);
        result.new_resources.forEach(resource => {
          setTerminalOutput(prev => [...prev, `  + ${resource.type}.${resource.name}`]);
          const meta = catalogResources.find(candidate => candidate.type === resource.type);
          addNode({
            type: resource.type,
            label: meta?.label ?? resource.type,
            icon: meta?.icon ?? '📦',
            defaults: resource.properties,
          });
        });
      }
      setChatMessages(prev => [...prev, { role: 'ai', text: `Plan fix: ${result.message}` }]);
      setLastCmdError(null);
    } catch {
      setTerminalOutput(prev => [...prev, '✦ AI fix analysis failed. Check that Ollama is running.']);
    }
    setFixLoading(false);
  }, [addNode, catalogResources, detectProvider, lastCmdError, nodes, setChatMessages, setFixLoading, setLastCmdError, setNodes, setTerminalOutput, tool]);

  const saveCodeToDisk = useCallback(async (value: string, context: {
    setCodeSaving: Dispatch<SetStateAction<boolean>>;
    isSyncing: MutableRefObject<boolean>;
    showNotification: (_message: string, _duration?: number) => void;
  }) => {
    if (!tool || !projectId) return;
    if (unresolvedHybridEnv) {
      context.showNotification(`Save failed: environment "${activeEnv}" has no configured IaC tool`, 5000);
      return;
    }
    if (!value.trim()) {
      context.showNotification('Nothing to save yet');
      return;
    }
    context.setCodeSaving(true);
    context.isSyncing.current = true;
    try {
      const fileName = concreteTool === 'pulumi' ? 'index.ts' : `main${TOOLS[concreteTool]?.ext || '.tf'}`;
      await api.syncCodeToDisk(projectId, tool, value, fileName, activeEnv);
      context.showNotification(`Saved ${fileName}`);
    } catch (err: any) {
      context.showNotification(`Save failed: ${err.message}`, 5000);
    } finally {
      context.setCodeSaving(false);
      setTimeout(() => { context.isSyncing.current = false; }, 1500);
    }
  }, [activeEnv, concreteTool, projectId, tool, unresolvedHybridEnv]);

  return {
    addNode,
    removeNode,
    updateProp,
    updateName,
    onMouseDown,
    onMouseMove,
    onMouseUp,
    handleSelectNode,
    handleSelectEdge,
    handleClearCanvasSelection,
    handleStartConnection,
    handleCancelConnection,
    handleCompleteConnection,
    detectProvider,
    handleChat,
    runCmd,
    fixLastError,
    saveCodeToDisk,
  };
}
