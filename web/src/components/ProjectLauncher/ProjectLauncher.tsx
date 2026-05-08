import { UIButton, UIInput, UIKicker, UILabel, UIPanel } from '../../ui';
import type { CatalogResource, FileEntry, ImportResult, ToolInfo } from '../../api';
import { ImportWizardModal } from '../ImportWizard';
import { PROJECT_CREATION_TOOLS, ALL_TOOLS, TOOLS } from '../../legacy';
import { S } from '../../styles';

export interface SavedProject {
  name: string;
  tool?: string;
  resources?: unknown[];
  updated_at?: string;
}

export interface ProjectLauncherProps {
  savedProjects: SavedProject[];
  detectedTools: ToolInfo[];
  projectName: string;
  showImportWizard: boolean;
  importTab: 'browse' | 'topology';
  browsePath: string;
  browseParent: string;
  browseEntries: FileEntry[];
  importPreview: ImportResult | null;
  importLoading: boolean;
  topologyDesc: string;
  topologyProvider: string;
  visionImages: File[];
  visionError: string | null;
  catalogResources: CatalogResource[];
  onProjectNameChange: (_name: string) => void;
  onCreateProject: (_tool: string) => void;
  onOpenProject: (_project: SavedProject) => void;
  onRevealProject: (_projectName: string) => void;
  onDeleteProject: (_projectName: string) => void;
  onStartImportBrowse: () => void;
  onStartTopology: () => void;
  onImportTabChange: (_tab: 'browse' | 'topology') => void;
  onBrowseLoaded: (_path: string, _entries: FileEntry[], _parent: string) => void;
  onImportPreviewChange: (_preview: ImportResult | null) => void;
  onImportLoadingChange: (_loading: boolean) => void;
  onTopologyDescChange: (_description: string) => void;
  onTopologyProviderChange: (_provider: string) => void;
  onVisionImagesChange: (_images: File[]) => void;
  onVisionErrorChange: (_error: string | null) => void;
  onGenerateTopology: () => void;
  onImportToCanvas: (_preview: ImportResult) => void;
  onCloseImportWizard: () => void;
}

export function ProjectLauncher({
  savedProjects,
  detectedTools,
  projectName,
  showImportWizard,
  importTab,
  browsePath,
  browseParent,
  browseEntries,
  importPreview,
  importLoading,
  topologyDesc,
  topologyProvider,
  visionImages,
  visionError,
  catalogResources,
  onProjectNameChange,
  onCreateProject,
  onOpenProject,
  onRevealProject,
  onDeleteProject,
  onStartImportBrowse,
  onStartTopology,
  onImportTabChange,
  onBrowseLoaded,
  onImportPreviewChange,
  onImportLoadingChange,
  onTopologyDescChange,
  onTopologyProviderChange,
  onVisionImagesChange,
  onVisionErrorChange,
  onGenerateTopology,
  onImportToCanvas,
  onCloseImportWizard,
}: ProjectLauncherProps) {
  const visibleSavedProjects = savedProjects.filter(project => project.tool).slice(0, 5);

  return (
    <div style={S.selectScreen} className="select-screen">
      <div className="ambient-orb ambient-orb-a" />
      <div className="ambient-orb ambient-orb-b" />
      <div style={S.selectBg} />
      <div style={S.selectContent}>
        <div style={S.logo}>
          <span style={{ fontSize: 28, color: 'var(--accent-action)' }}>◆</span>
          <span style={S.logoText}>IaC Studio</span>
        </div>

        {visibleSavedProjects.length > 0 && (
          <div style={{ marginBottom: 32, width: '100%', maxWidth: 600 }}>
            <UIKicker style={{ marginBottom: 12 }}>Recent Projects</UIKicker>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              {visibleSavedProjects.map(project => {
                const toolMeta = ALL_TOOLS[project.tool || 'terraform'] || TOOLS.terraform;
                const count = project.resources?.length || 0;
                const updated = project.updated_at ? ` · ${new Date(project.updated_at).toLocaleDateString()}` : '';

                return (
                  <div
                    key={project.name}
                    className="tool-card"
                    style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '10px 16px', background: 'var(--bg-elev-1)', border: '1px solid var(--border-main)', borderRadius: 10, textAlign: 'left', transition: 'border-color 0.2s' }}
                    onMouseEnter={event => { event.currentTarget.style.borderColor = toolMeta.color; }}
                    onMouseLeave={event => { event.currentTarget.style.borderColor = 'var(--border-main)'; }}
                  >
                    <button
                      type="button"
                      aria-label={`Open project ${project.name}`}
                      style={{ flex: 1, display: 'flex', alignItems: 'center', gap: 12, minWidth: 0, padding: 0, background: 'transparent', border: 0, cursor: 'pointer', textAlign: 'left' }}
                      onClick={() => onOpenProject(project)}
                    >
                      <span style={{ fontSize: 20 }}>{toolMeta.icon}</span>
                      <span style={{ flex: 1, minWidth: 0 }}>
                        <span style={{ display: 'block', fontSize: 14, fontWeight: 600, color: '#ccc', fontFamily: 'JetBrains Mono' }}>{project.name}</span>
                        <span style={{ display: 'block', fontSize: 11, color: '#555' }}>
                          {toolMeta.name} · {count} resource{count !== 1 ? 's' : ''}{updated}
                        </span>
                      </span>
                      <span style={{ fontSize: 11, color: toolMeta.color, fontWeight: 700, fontFamily: 'JetBrains Mono' }}>OPEN</span>
                    </button>
                    <button
                      type="button"
                      style={{ background: 'transparent', border: 0, fontSize: 11, color: '#555', cursor: 'pointer', fontFamily: 'JetBrains Mono', padding: '2px 4px' }}
                      title="Open in file manager"
                      onClick={() => onRevealProject(project.name)}
                    >
                      FILES
                    </button>
                    <button
                      type="button"
                      style={{ background: 'transparent', border: 0, fontSize: 12, color: '#555', cursor: 'pointer', padding: '0 8px' }}
                      title="Delete project"
                      aria-label={`Delete project ${project.name}`}
                      onClick={() => {
                        if (!confirm(`Delete project "${project.name}"?\n\nThis will permanently remove the project directory and all its files.\n\nThis cannot be undone.`)) return;
                        onDeleteProject(project.name);
                      }}
                    >
                      ✕
                    </button>
                  </div>
                );
              })}
            </div>
          </div>
        )}

        <h1 style={S.title}>{savedProjects.length > 0 ? 'New Project' : 'Choose your IaC tool'}</h1>
        <p style={S.subtitle}>Visual infrastructure builder with AI-powered assistance</p>
        <div style={S.cardGrid}>
          {Object.entries(PROJECT_CREATION_TOOLS).map(([key, toolMeta]) => {
            const detected = detectedTools.find(tool => tool.name === toolMeta.name);
            return (
              <button
                key={key}
                className="tool-card panel-reveal"
                style={{ ...S.card, borderColor: toolMeta.color + '33' }}
                onClick={() => onCreateProject(key)}
                onMouseEnter={event => {
                  event.currentTarget.style.borderColor = toolMeta.color;
                  event.currentTarget.style.transform = 'translateY(-4px)';
                }}
                onMouseLeave={event => {
                  event.currentTarget.style.borderColor = toolMeta.color + '33';
                  event.currentTarget.style.transform = 'translateY(0)';
                }}
              >
                <span style={{ fontSize: 26, fontWeight: 700, letterSpacing: 0.8, fontFamily: 'JetBrains Mono', color: toolMeta.color }}>{toolMeta.icon}</span>
                <span style={{ fontSize: 18, fontWeight: 600, color: toolMeta.color }}>{toolMeta.name}</span>
                <span style={{ fontSize: 12, color: '#555', fontFamily: 'JetBrains Mono' }}>{toolMeta.ext} files</span>
                {detected && (
                  <span style={{ fontSize: 10, color: detected.available ? '#4ade80' : '#666', marginTop: 4 }}>
                    {detected.available ? `✓ ${detected.version?.slice(0, 30)}` : '✗ Not installed'}
                  </span>
                )}
              </button>
            );
          })}
        </div>
        <div style={S.features}>
          {['Visual drag-and-drop builder', 'AI chat to generate resources', 'Real-time code generation', 'Files editable on disk'].map(feature => (
            <div key={feature} style={{ fontSize: 13, color: '#555', display: 'flex', alignItems: 'center', gap: 6 }}>
              <span style={{ fontSize: 8, color: 'var(--accent-action)' }}>●</span> {feature}
            </div>
          ))}
        </div>

        <UIPanel style={{ marginTop: 32, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 10, padding: '16px 24px', width: '100%', maxWidth: 480 }}>
          <div style={{ display: 'flex', gap: 8, alignItems: 'center', width: '100%' }}>
            <UILabel style={{ whiteSpace: 'nowrap' }}>Project name:</UILabel>
            <UIInput
              style={{ flex: 1, minWidth: 0 }}
              value={projectName}
              aria-label="Project name"
              onChange={event => onProjectNameChange(event.target.value)}
              placeholder="my-infra-project"
            />
          </div>
          <div className="ui-path">
            ROOT ~/iac-projects/<span style={{ color: 'var(--accent-action)' }}>{projectName}</span>/
          </div>

          <div style={{ marginTop: 12, display: 'flex', gap: 10 }}>
            <UIButton onClick={onStartImportBrowse}>
              Import Existing Project
            </UIButton>
            <UIButton variant="primary" onClick={onStartTopology}>
              Build with AI
            </UIButton>
          </div>
        </UIPanel>

        {showImportWizard && (
          <ImportWizardModal
            importTab={importTab}
            onImportTabChange={onImportTabChange}
            browsePath={browsePath}
            browseParent={browseParent}
            browseEntries={browseEntries}
            onBrowseLoaded={onBrowseLoaded}
            importPreview={importPreview}
            onImportPreviewChange={onImportPreviewChange}
            importLoading={importLoading}
            onImportLoadingChange={onImportLoadingChange}
            topologyDesc={topologyDesc}
            onTopologyDescChange={onTopologyDescChange}
            topologyProvider={topologyProvider}
            onTopologyProviderChange={onTopologyProviderChange}
            visionImages={visionImages}
            onVisionImagesChange={onVisionImagesChange}
            visionError={visionError}
            onVisionErrorChange={onVisionErrorChange}
            catalogResources={catalogResources}
            onGenerateTopology={onGenerateTopology}
            onImportToCanvas={onImportToCanvas}
            onClose={onCloseImportWizard}
          />
        )}
      </div>
    </div>
  );
}
