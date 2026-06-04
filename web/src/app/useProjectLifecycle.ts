import { useCallback, useEffect, useRef, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { api, type Resource, type ToolInfo } from '../api';
import type { SavedProject } from '../components/ProjectLauncher';
import type { CanvasMode } from '../components/Canvas';
import { envForResourceLoad, shouldParseResourcesFromDisk } from '../projectLoad';
import type { Edge } from '../legacy';
import type { LayeredProject } from '../types';
import { extractLayoutMeta, normalizeLayeredProject } from './layered';
import type { AppResource, ChatMessage } from './types';

interface UseProjectLifecycleInput {
  savedProject: any;
  tool: string | null;
  projectId: string;
  projectName: string;
  projectLayoutMeta: Record<string, any> | null;
  nodes: AppResource[];
  edges: Edge[];
  hasCreatedProject: MutableRefObject<boolean>;
  initialLoadDone: MutableRefObject<boolean>;
  resetNodes: (_nodes: AppResource[]) => void;
  setDetectedTools: Dispatch<SetStateAction<ToolInfo[]>>;
  setSavedProjects: Dispatch<SetStateAction<SavedProject[]>>;
  setProjectName: Dispatch<SetStateAction<string>>;
  setProjectId: Dispatch<SetStateAction<string>>;
  setTool: Dispatch<SetStateAction<string | null>>;
  setEdges: Dispatch<SetStateAction<Edge[]>>;
  setChatMessages: Dispatch<SetStateAction<ChatMessage[]>>;
  setTerminalOutput: Dispatch<SetStateAction<string[]>>;
  setProjectLayoutMeta: Dispatch<SetStateAction<Record<string, any> | null>>;
  setLayeredProject: Dispatch<SetStateAction<LayeredProject | null>>;
  setActiveEnvironment: Dispatch<SetStateAction<string | null>>;
  setCanvasMode: Dispatch<SetStateAction<CanvasMode>>;
  showNotification: (_message: string, _duration?: number) => void;
}

export function useProjectLifecycle({
  savedProject,
  tool,
  projectId,
  projectName,
  projectLayoutMeta,
  nodes,
  edges,
  hasCreatedProject,
  initialLoadDone,
  resetNodes,
  setDetectedTools,
  setSavedProjects,
  setProjectName,
  setProjectId,
  setTool,
  setEdges,
  setChatMessages,
  setTerminalOutput,
  setProjectLayoutMeta,
  setLayeredProject,
  setActiveEnvironment,
  setCanvasMode,
  showNotification,
}: UseProjectLifecycleInput) {
  const buildPersistedState = useCallback(() => ({
    ...(projectLayoutMeta || {}),
    tool,
    resources: nodes.map(node => ({
      id: node.id,
      type: node.type,
      name: node.name,
      label: node.label,
      icon: node.icon,
      properties: node.properties,
      file: node.file,
      line: node.line,
      x: node.x,
      y: node.y,
      connections: edges.filter(edge => edge.from === node.id).map(edge => ({
        target_id: edge.to,
        field: edge.field,
        label: edge.label,
      })),
    })),
  }), [edges, nodes, projectLayoutMeta, tool]);

  const applyProjectState = useCallback((state: any) => {
    const meta = extractLayoutMeta(state);
    const layered = normalizeLayeredProject(meta);
    setProjectLayoutMeta(meta);
    setLayeredProject(layered);
    setActiveEnvironment(current => {
      if (!layered) return null;
      if (current && layered.environments.includes(current)) return current;
      return layered.environments[0];
    });
    setCanvasMode(layered ? 'swimlane' : 'freeform');

    if (state?.resources?.length > 0) {
      resetNodes(state.resources.map((resource: any) => ({
        id: resource.id || `res_${Math.random().toString(36).slice(2)}`,
        type: resource.type,
        name: resource.name,
        label: resource.label || resource.type,
        icon: resource.icon || '📦',
        properties: resource.properties || {},
        file: resource.file,
        line: resource.line,
        x: resource.x ?? 80 + Math.random() * 300,
        y: resource.y ?? 80 + Math.random() * 200,
      })));
      const restoredEdges: Edge[] = [];
      for (const resource of state.resources) {
        for (const connection of resource.connections || []) {
          restoredEdges.push({
            id: `${resource.id}->${connection.target_id}:${connection.field}`,
            from: resource.id,
            to: connection.target_id,
            fromType: resource.type,
            toType: state.resources.find((candidate: any) => candidate.id === connection.target_id)?.type || '',
            field: connection.field,
            label: connection.label || connection.field,
          });
        }
      }
      setEdges(restoredEdges);
    } else {
      resetNodes([]);
      setEdges([]);
    }
  }, [resetNodes, setActiveEnvironment, setCanvasMode, setEdges, setLayeredProject, setProjectLayoutMeta]);

  const applyParsedResources = useCallback((resources: Resource[]) => {
    resetNodes(resources.map((resource, index) => ({
      ...resource,
      id: resource.id || `${resource.type}.${resource.name || index}`,
      label: resource.label || resource.type,
      icon: resource.icon || '📦',
      properties: resource.properties || {},
      x: resource.x ?? 80 + (index % 5) * 200,
      y: resource.y ?? 80 + Math.floor(index / 5) * 130,
    })));
    setEdges([]);
  }, [resetNodes, setEdges]);

  useEffect(() => {
    api.detectTools().then(setDetectedTools).catch(() => {});
    api.listProjectStates().then(setSavedProjects).catch(() => {});
    if (savedProject?.projectId && savedProject?.tool) {
      hasCreatedProject.current = true;
      initialLoadDone.current = false;
      api.loadState(savedProject.projectId).then(state => {
        applyProjectState(state);
        const selectedTool = state?.tool || savedProject.tool;
        const selectedLayered = normalizeLayeredProject(extractLayoutMeta(state));
        setTool(selectedTool);
        if (shouldParseResourcesFromDisk(state)) {
          api.getResources(savedProject.projectId, selectedTool, envForResourceLoad(selectedTool, selectedLayered)).then(applyParsedResources).catch(() => {});
        }
      }).catch(() => {});
    }
  }, [applyParsedResources, applyProjectState, hasCreatedProject, initialLoadDone, savedProject, setDetectedTools, setSavedProjects, setTool]);

  useEffect(() => {
    if (tool && projectId) {
      localStorage.setItem('iac-studio-session', JSON.stringify({ tool, projectId, projectName }));
    } else {
      localStorage.removeItem('iac-studio-session');
    }
  }, [projectId, projectName, tool]);

  const saveTimer = useRef<ReturnType<typeof setTimeout>>();
  useEffect(() => {
    if (!tool || !projectId || !hasCreatedProject.current) return;
    clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      api.saveState(projectId, buildPersistedState()).catch(() => {});
    }, 2000);
  }, [buildPersistedState, hasCreatedProject, projectId, tool]);

  const openProject = useCallback(async (project: SavedProject) => {
    setProjectName(project.name);
    setProjectId(project.name);
    setTool(project.tool || 'terraform');
    hasCreatedProject.current = true;
    try {
      const state = await api.loadState(project.name);
      applyProjectState(state);
      const selectedTool = state?.tool || project.tool || 'terraform';
      const selectedLayered = normalizeLayeredProject(extractLayoutMeta(state));
      setTool(selectedTool);
      if (shouldParseResourcesFromDisk(state)) {
        const parsed = await api.getResources(project.name, selectedTool, envForResourceLoad(selectedTool, selectedLayered));
        applyParsedResources(parsed);
      }
      showNotification(`Opened project: ${project.name}`);
    } catch {
      setProjectLayoutMeta(null);
      setLayeredProject(null);
      setActiveEnvironment(null);
      setCanvasMode('freeform');
    }
  }, [applyParsedResources, applyProjectState, hasCreatedProject, setActiveEnvironment, setCanvasMode, setLayeredProject, setProjectId, setProjectLayoutMeta, setProjectName, setTool, showNotification]);

  const handleBackToProjectSelect = useCallback(async () => {
    if (projectId && hasCreatedProject.current) {
      await api.saveState(projectId, buildPersistedState()).catch(() => {});
    }
    api.listProjectStates().then(setSavedProjects).catch(() => {});
    setTool(null);
    resetNodes([]);
    setEdges([]);
    setChatMessages([]);
    setTerminalOutput([]);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    initialLoadDone.current = false;
    hasCreatedProject.current = false;
  }, [buildPersistedState, hasCreatedProject, initialLoadDone, projectId, resetNodes, setActiveEnvironment, setCanvasMode, setChatMessages, setEdges, setLayeredProject, setProjectLayoutMeta, setSavedProjects, setTerminalOutput, setTool]);

  const handleCreateProject = useCallback(async (selectedTool: string) => {
    setTool(selectedTool);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    setProjectId(projectName);
    hasCreatedProject.current = true;
    initialLoadDone.current = true;
    try {
      await api.createProject(projectName, selectedTool);
    } catch {
      // Backend might not be running, continue with local-only mode.
    }
  }, [hasCreatedProject, initialLoadDone, projectName, setActiveEnvironment, setCanvasMode, setLayeredProject, setProjectId, setProjectLayoutMeta, setTool]);

  return { openProject, handleBackToProjectSelect, handleCreateProject };
}
