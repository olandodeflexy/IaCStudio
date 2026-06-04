import { useCallback, useState, type Dispatch, type MutableRefObject, type SetStateAction } from 'react';
import { api, type CatalogResource, type FileEntry, type ImportResult } from '../api';
import { errorMessage } from '../lib/errors';
import { edgeId, type Edge } from '../legacy';
import type { LayeredProject } from '../types';
import type { AppResource, ChatMessage } from './types';

export interface UseImportWorkflowInput {
  detectedTools: { name: string; available: boolean }[];
  projectName: string;
  catalogResources: CatalogResource[];
  resetNodes: (_nodes: AppResource[]) => void;
  setEdges: Dispatch<SetStateAction<Edge[]>>;
  setTool: Dispatch<SetStateAction<string | null>>;
  setProjectId: Dispatch<SetStateAction<string>>;
  setProjectLayoutMeta: Dispatch<SetStateAction<Record<string, any> | null>>;
  setLayeredProject: Dispatch<SetStateAction<LayeredProject | null>>;
  setActiveEnvironment: Dispatch<SetStateAction<string | null>>;
  setCanvasMode: Dispatch<SetStateAction<'freeform' | 'swimlane'>>;
  setChatMessages: Dispatch<SetStateAction<ChatMessage[]>>;
  hasCreatedProject: MutableRefObject<boolean>;
  initialLoadDone: MutableRefObject<boolean>;
  showNotification: (_message: string, _duration?: number) => void;
  showPersistentNotification: (_message: string) => void;
  clearNotification: () => void;
}

export function useImportWorkflow({
  detectedTools,
  projectName,
  catalogResources,
  resetNodes,
  setEdges,
  setTool,
  setProjectId,
  setProjectLayoutMeta,
  setLayeredProject,
  setActiveEnvironment,
  setCanvasMode,
  setChatMessages,
  hasCreatedProject,
  initialLoadDone,
  showNotification,
  showPersistentNotification,
  clearNotification,
}: UseImportWorkflowInput) {
  const [showImportWizard, setShowImportWizard] = useState(false);
  const [importTab, setImportTab] = useState<'browse' | 'topology'>('browse');
  const [browsePath, setBrowsePath] = useState('');
  const [browseEntries, setBrowseEntries] = useState<FileEntry[]>([]);
  const [browseParent, setBrowseParent] = useState('');
  const [importPreview, setImportPreview] = useState<ImportResult | null>(null);
  const [importLoading, setImportLoading] = useState(false);
  const [topologyDesc, setTopologyDesc] = useState('');
  const [topologyProvider, setTopologyProvider] = useState('aws');
  const [visionImages, setVisionImages] = useState<File[]>([]);
  const [visionError, setVisionError] = useState<string | null>(null);

  const closeImportWizard = useCallback(() => {
    setShowImportWizard(false);
    setImportPreview(null);
    setVisionImages([]);
    setVisionError(null);
  }, []);

  const handleBrowseLoaded = useCallback((path: string, entries: FileEntry[], parent: string) => {
    setBrowsePath(path);
    setBrowseEntries(entries);
    setBrowseParent(parent);
  }, []);

  const loadBrowsePath = useCallback(async (path?: string) => {
    try {
      const result = await api.browse(path);
      handleBrowseLoaded(result.path, result.entries, result.parent);
    } catch {
      // Preserve local-only behavior when the backend browse endpoint is unavailable.
    }
  }, [handleBrowseLoaded]);

  const startImportBrowse = useCallback(() => {
    setImportTab('browse');
    setVisionImages([]);
    setVisionError(null);
    setShowImportWizard(true);
    loadBrowsePath();
  }, [loadBrowsePath]);

  const startTopologyBuilder = useCallback(() => {
    setImportTab('topology');
    setShowImportWizard(true);
  }, []);

  const handleGenerateTopology = useCallback(async () => {
    const toolKey = detectedTools.find(tool => tool.available && tool.name !== 'Ansible')?.name === 'OpenTofu' ? 'opentofu' : 'terraform';

    if (visionImages.length > 0) {
      setImportLoading(true);
      showPersistentNotification('AI is reading your diagram...');
      try {
        const result = await api.generateTopologyFromImages({
          description: topologyDesc,
          tool: toolKey,
          provider: topologyProvider,
          images: visionImages,
        });
        if (result.message) {
          setChatMessages(prev => [...prev, { role: 'ai', text: `Diagram analysis: ${result.message}` }]);
        }
        setImportPreview({
          tool: toolKey,
          provider: topologyProvider,
          files: visionImages.map(file => ({ path: file.name, name: file.name, type: file.type, size: file.size })),
          resources: result.resources || [],
          edges: [],
          summary: result.message || 'Infrastructure generated from diagram',
        });
      } catch (err: any) {
        setImportPreview({
          tool: 'unknown',
          provider: topologyProvider,
          files: [],
          resources: [],
          edges: [],
          summary: err.message || 'Diagram analysis failed',
          warnings: [err.message || 'Diagram analysis failed'],
        });
      } finally {
        setImportLoading(false);
        clearNotification();
      }
      return;
    }

    if (!topologyDesc.trim()) return;
    setImportLoading(true);
    showPersistentNotification('AI is designing your infrastructure...');
    try {
      await api.generateTopology(topologyDesc, toolKey, topologyProvider);
    } catch (err: any) {
      const message = err?.message || 'Generation failed';
      setImportPreview({ tool: 'unknown', provider: 'unknown', files: [], resources: [], edges: [], summary: message, warnings: [message] });
      setImportLoading(false);
      clearNotification();
    }
  }, [clearNotification, detectedTools, setChatMessages, showPersistentNotification, topologyDesc, topologyProvider, visionImages]);

  const handleImportToCanvas = useCallback(async (preview: ImportResult) => {
    const selectedTool = preview.tool === 'opentofu' ? 'opentofu' : preview.tool === 'ansible' ? 'ansible' : 'terraform';
    try {
      await api.createProject(projectName, selectedTool);
    } catch (err: unknown) {
      showNotification(`Import failed: ${errorMessage(err, 'Unable to create project')}`, 5000);
      return;
    }
    setTool(selectedTool);
    setProjectId(projectName);
    setProjectLayoutMeta(null);
    setLayeredProject(null);
    setActiveEnvironment(null);
    setCanvasMode('freeform');
    hasCreatedProject.current = true;
    initialLoadDone.current = true;

    const catalogByType = new Map(catalogResources.map(resource => [resource.type, resource]));
    const generatedIdPrefix = `imp_${Date.now()}`;
    const imported = preview.resources.map((resource, index) => {
      const id = resource.id || `${generatedIdPrefix}_${index}`;
      const meta = catalogByType.get(resource.type);
      const { file: _file, line: _line, ...rest } = resource;
      return {
        ...rest,
        id,
        x: 80 + (index % 5) * 200,
        y: 80 + Math.floor(index / 5) * 130,
        icon: meta?.icon ?? '📦',
        label: meta?.label ?? resource.type,
      };
    });
    resetNodes(imported);

    const nodeTypeById = new Map(imported.map(resource => [resource.id, resource.type]));
    const newEdges = preview.edges.flatMap(edge => {
      const from = edge.from_id;
      const to = edge.to_id;
      const fromType = nodeTypeById.get(from);
      const toType = nodeTypeById.get(to);
      if (!fromType || !toType) return [];
      return [{
        id: edgeId(from, to, edge.field),
        from,
        to,
        fromType,
        toType,
        field: edge.field,
        label: edge.field.replace(/_/g, ' '),
      }];
    });
    setEdges(newEdges);

    setShowImportWizard(false);
    setImportPreview(null);
    setVisionImages([]);
    setVisionError(null);
    showNotification(`Imported ${preview.resources.length} resources`, 4000);
  }, [catalogResources, hasCreatedProject, initialLoadDone, projectName, resetNodes, setActiveEnvironment, setCanvasMode, setEdges, setLayeredProject, setProjectId, setProjectLayoutMeta, setTool, showNotification]);

  return {
    showImportWizard,
    importTab,
    browsePath,
    browseEntries,
    browseParent,
    importPreview,
    importLoading,
    topologyDesc,
    topologyProvider,
    visionImages,
    visionError,
    setImportTab,
    setImportPreview,
    setImportLoading,
    setTopologyDesc,
    setTopologyProvider,
    setVisionImages,
    setVisionError,
    handleBrowseLoaded,
    startImportBrowse,
    startTopologyBuilder,
    handleGenerateTopology,
    handleImportToCanvas,
    closeImportWizard,
  };
}
