import { api, type CatalogResource, type FileEntry, type ImportResult } from '../../api';
import { fileGlyph } from '../../legacy';
import { UIButton, UILabel, UIModal, UITextArea } from '../../ui';
import { VisionDropzone } from '../VisionDropzone';

export type ImportWizardTab = 'browse' | 'topology';

export interface ImportWizardModalProps {
  importTab: ImportWizardTab;
  onImportTabChange: (_tab: ImportWizardTab) => void;
  browsePath: string;
  browseParent: string;
  browseEntries: FileEntry[];
  onBrowseLoaded: (_path: string, _entries: FileEntry[], _parent: string) => void;
  importPreview: ImportResult | null;
  onImportPreviewChange: (_preview: ImportResult | null) => void;
  importLoading: boolean;
  onImportLoadingChange: (_loading: boolean) => void;
  topologyDesc: string;
  onTopologyDescChange: (_description: string) => void;
  topologyProvider: string;
  onTopologyProviderChange: (_provider: string) => void;
  visionImages: File[];
  onVisionImagesChange: (_images: File[]) => void;
  visionError: string | null;
  onVisionErrorChange: (_error: string | null) => void;
  catalogResources: CatalogResource[];
  onGenerateTopology: () => void;
  onImportToCanvas: (_preview: ImportResult) => Promise<void> | void;
  onClose: () => void;
}

const providerOptions = ['aws', 'google', 'azurerm'];

function providerLabel(provider: string) {
  if (provider === 'aws') return 'AWS';
  if (provider === 'google') return 'GCP';
  return 'Azure';
}

export function ImportWizardModal({
  importTab,
  onImportTabChange,
  browsePath,
  browseParent,
  browseEntries,
  onBrowseLoaded,
  importPreview,
  onImportPreviewChange,
  importLoading,
  onImportLoadingChange,
  topologyDesc,
  onTopologyDescChange,
  topologyProvider,
  onTopologyProviderChange,
  visionImages,
  onVisionImagesChange,
  visionError,
  onVisionErrorChange,
  catalogResources,
  onGenerateTopology,
  onImportToCanvas,
  onClose,
}: ImportWizardModalProps) {
  const browseTo = async (path?: string) => {
    try {
      const result = await api.browse(path);
      onBrowseLoaded(result.path, result.entries, result.parent);
    } catch {
      // Keep the existing quiet failure behavior for unavailable local browsing.
    }
  };

  const importFolder = async () => {
    onImportLoadingChange(true);
    try {
      const result = await api.importProject(browsePath);
      onImportPreviewChange(result);
    } catch (err: any) {
      const message = err?.message || 'Import failed';
      onImportPreviewChange({
        tool: 'unknown',
        provider: 'unknown',
        files: [],
        resources: [],
        edges: [],
        summary: message,
        warnings: [message],
      });
    } finally {
      onImportLoadingChange(false);
    }
  };

  const changeTab = (tab: ImportWizardTab) => {
    onImportTabChange(tab);
    onImportPreviewChange(null);
    if (tab === 'browse') {
      onVisionImagesChange([]);
      onVisionErrorChange(null);
    }
  };

  return (
    <UIModal onClose={onClose}>
      <div className="ui-modal-header">
        <div style={{ display: 'flex', gap: 12 }}>
          {(['browse', 'topology'] as const).map(tab => (
            <UIButton
              key={tab}
              variant="tab"
              active={importTab === tab}
              onClick={() => changeTab(tab)}
            >
              {tab === 'browse' ? 'Browse Files' : 'AI Topology'}
            </UIButton>
          ))}
        </div>
        <button className="ui-close" onClick={onClose}>×</button>
      </div>

      {importTab === 'browse' && !importPreview && (
        <div style={{ flex: 1, overflow: 'auto', minHeight: 300 }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 20px', borderBottom: '1px solid var(--border-soft)', background: 'var(--bg-elev-1)' }}>
            <UIButton onClick={() => browseTo(browseParent)}>
              ↑
            </UIButton>
            <span style={{ fontSize: 11, color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{browsePath}</span>
            <UIButton
              variant="primary"
              disabled={importLoading}
              onClick={importFolder}
            >
              {importLoading ? 'Scanning...' : 'Import this folder'}
            </UIButton>
          </div>
          <div style={{ padding: '4px 0' }}>
            {browseEntries.map(entry => (
              <div
                key={entry.path}
                style={{ display: 'flex', alignItems: 'center', gap: 10, padding: '7px 20px', cursor: entry.is_dir ? 'pointer' : 'default', fontSize: 13, color: entry.is_dir ? '#ccc' : '#777', fontFamily: 'JetBrains Mono' }}
                onClick={() => {
                  if (entry.is_dir) {
                    browseTo(entry.path);
                  }
                }}
                onMouseEnter={event => {
                  if (entry.is_dir) (event.currentTarget as any).style.background = 'var(--bg-elev-2)';
                }}
                onMouseLeave={event => {
                  (event.currentTarget as any).style.background = 'transparent';
                }}
              >
                <span style={{ fontSize: 10, fontFamily: 'JetBrains Mono', color: '#7b8d84', minWidth: 30 }}>{fileGlyph(entry)}</span>
                <span style={{ flex: 1 }}>{entry.name}</span>
                {entry.is_dir && entry.children !== undefined && <span style={{ color: '#444', fontSize: 10 }}>{entry.children} items</span>}
                {!entry.is_dir && <span style={{ color: '#444', fontSize: 10 }}>{entry.size > 1024 ? Math.round(entry.size / 1024) + 'KB' : entry.size + 'B'}</span>}
              </div>
            ))}
            {browseEntries.length === 0 && <div style={{ padding: 20, textAlign: 'center', color: '#444' }}>Empty directory</div>}
          </div>
        </div>
      )}

      {importTab === 'topology' && !importPreview && (
        <div style={{ flex: 1, padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>
          <div style={{ fontSize: 14, color: 'var(--text-main)', fontWeight: 600 }}>Describe your infrastructure</div>
          <div className="ui-note">
            Tell us what you want to build in plain language, or upload a diagram for vision analysis.
          </div>
          <VisionDropzone
            files={visionImages}
            onFilesChange={onVisionImagesChange}
            onError={onVisionErrorChange}
            error={visionError}
            disabled={importLoading}
          />
          <UITextArea
            style={{ flex: 1, minHeight: 120 }}
            value={topologyDesc}
            onChange={event => onTopologyDescChange(event.target.value)}
            placeholder={"Optional context:\nA three-tier web app with VPC, ALB, auto-scaling EC2, RDS PostgreSQL, and S3 for static assets\nA GKE cluster with Cloud SQL, Redis cache, and Cloud Storage for a microservices platform"}
          />
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <UILabel>Provider:</UILabel>
            {providerOptions.map(provider => (
              <UIButton
                key={provider}
                variant="tab"
                active={topologyProvider === provider}
                onClick={() => onTopologyProviderChange(provider)}
              >
                {providerLabel(provider)}
              </UIButton>
            ))}
          </div>
          <UIButton
            variant="primary"
            disabled={(!topologyDesc.trim() && visionImages.length === 0) || Boolean(visionError) || importLoading}
            onClick={onGenerateTopology}
          >
            {importLoading ? 'Generating... (this may take a minute)' : visionImages.length > 0 ? 'Generate from Diagram' : 'Generate Infrastructure'}
          </UIButton>
        </div>
      )}

      {importPreview && (
        <div style={{ flex: 1, overflow: 'auto', padding: 20, display: 'flex', flexDirection: 'column', gap: 12 }}>
          <div style={{ fontSize: 14, fontWeight: 600, color: '#bbb' }}>
            {importPreview.tool === 'unknown' ? 'Import Failed' : 'Preview'}
          </div>
          <div className="ui-note">{importPreview.summary}</div>

          {importPreview.warnings && importPreview.warnings.length > 0 && (
            <div style={{ background: '#ef444411', border: '1px solid #ef444433', borderRadius: 8, padding: 10 }}>
              {importPreview.warnings.map((warning, index) => (
                <div key={index} style={{ fontSize: 11, color: '#ef4444', fontFamily: 'JetBrains Mono' }}>{warning}</div>
              ))}
            </div>
          )}

          {importPreview.resources.length > 0 && (
            <>
              <div style={{ fontSize: 11, color: '#555', fontFamily: 'JetBrains Mono', textTransform: 'uppercase', letterSpacing: 1 }}>
                {importPreview.resources.length} Resources
              </div>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                {importPreview.resources.map((resource, index) => {
                  const meta = catalogResources.find(candidate => candidate.type === resource.type);
                  return (
                    <span key={index} style={{ background: 'var(--bg-elev-2)', borderRadius: 6, padding: '4px 10px', fontSize: 11, color: 'var(--text-main)', fontFamily: 'JetBrains Mono' }}>
                      {meta?.icon ?? '📦'} {resource.type}.{resource.name}
                    </span>
                  );
                })}
              </div>
              {importPreview.edges.length > 0 && (
                <div style={{ fontSize: 11, color: '#555', fontFamily: 'JetBrains Mono' }}>
                  {importPreview.edges.length} connections detected
                </div>
              )}
            </>
          )}

          <div style={{ display: 'flex', gap: 10, marginTop: 8 }}>
            <UIButton onClick={() => onImportPreviewChange(null)}>
              ← Back
            </UIButton>
            {importPreview.resources.length > 0 && (
              <UIButton variant="primary" onClick={() => onImportToCanvas(importPreview)}>
                Import to Canvas
              </UIButton>
            )}
          </div>
        </div>
      )}
    </UIModal>
  );
}
