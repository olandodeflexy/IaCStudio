import type { CatalogResource, Suggestion } from '../../api';
import { S } from '../../styles';

export type SidebarPanel = 'palette' | 'files' | 'suggest' | 'guide';

export interface SidebarToolMeta {
  color: string;
  ext: string;
}

export interface WorkspaceSidebarProps {
  width: number;
  activePanel: SidebarPanel;
  tool: string;
  toolMeta: SidebarToolMeta;
  projectName: string;
  provider: string;
  resources: CatalogResource[];
  suggestions: Suggestion[];
  searchQuery: string;
  onActivePanelChange: (_panel: SidebarPanel) => void;
  onSearchQueryChange: (_query: string) => void;
  onAddResource: (_resource: CatalogResource) => void;
  onResourceHover: (_resource: CatalogResource, _position: { x: number; y: number }) => void;
  onResourceHoverEnd: () => void;
}

function guideFoundation(tool: string, provider: string) {
  if (tool === 'ansible') return 'Start with package installation (apt/yum)';
  if (provider === 'google') return 'Start with a VPC Network (google_compute_network)';
  if (provider === 'azurerm') return 'Start with a Resource Group (azurerm_resource_group)';
  return 'Start with a VPC (aws_vpc)';
}

export function WorkspaceSidebar({
  width,
  activePanel,
  tool,
  toolMeta,
  projectName,
  provider,
  resources,
  suggestions,
  searchQuery,
  onActivePanelChange,
  onSearchQueryChange,
  onAddResource,
  onResourceHover,
  onResourceHoverEnd,
}: WorkspaceSidebarProps) {
  const normalizedSearch = searchQuery.trim().toLowerCase();
  const filteredResources = normalizedSearch
    ? resources.filter(resource =>
        resource.label.toLowerCase().includes(normalizedSearch) ||
        resource.type.toLowerCase().includes(normalizedSearch) ||
        resource.category.toLowerCase().includes(normalizedSearch))
    : resources;
  const filteredCategories = [...new Set(filteredResources.map(resource => resource.category))];

  return (
    <aside style={{ ...S.sidebar, width }}>
      <div style={S.tabs}>
        {[
          { key: 'palette' as const, label: 'Resources' },
          { key: 'suggest' as const, label: 'Next' },
          { key: 'guide' as const, label: 'Guide' },
        ].map(tab => (
          <button
            key={tab.key}
            style={{ ...S.tab, ...(activePanel === tab.key ? { color: toolMeta.color, borderBottomColor: toolMeta.color } : {}), fontSize: 10 }}
            aria-label={tab.key === 'suggest' && suggestions.length > 0 ? `${tab.label} ${suggestions.length}` : tab.label}
            onClick={() => onActivePanelChange(tab.key)}
          >
            {tab.label}
            {tab.key === 'suggest' && suggestions.length > 0 && (
              <span style={{ marginLeft: 4, background: toolMeta.color + '33', color: toolMeta.color, padding: '1px 5px', borderRadius: 8, fontSize: 9 }}>
                {suggestions.length}
              </span>
            )}
          </button>
        ))}
      </div>

      {activePanel === 'palette' && (
        <>
          <div style={{ padding: '8px 10px', borderBottom: '1px solid var(--border-soft)' }}>
            <input
              style={{ ...S.finput, fontSize: 12, padding: '6px 10px', background: 'var(--bg-app)' }}
              placeholder="Search resources..."
              aria-label="Search resources"
              value={searchQuery}
              onChange={event => onSearchQueryChange(event.target.value)}
            />
            {searchQuery && (
              <div style={{ fontSize: 10, color: '#555', marginTop: 4, fontFamily: 'JetBrains Mono' }}>
                {filteredResources.length} result{filteredResources.length !== 1 ? 's' : ''}
              </div>
            )}
          </div>
          <div style={S.palScroll}>
            {filteredCategories.map(category => (
              <div key={category}>
                <div style={S.catTitle}>{category}</div>
                {filteredResources.filter(resource => resource.category === category).map(resource => (
                  <button
                    key={resource.type}
                    style={S.palItem}
                    aria-label={`Add ${resource.label}`}
                    onClick={() => onAddResource(resource)}
                    onMouseEnter={event => {
                      event.currentTarget.style.background = 'var(--bg-elev-2)';
                      const rect = event.currentTarget.getBoundingClientRect();
                      onResourceHover(resource, { x: rect.right + 8, y: rect.top });
                    }}
                    onMouseLeave={event => {
                      event.currentTarget.style.background = 'transparent';
                      onResourceHoverEnd();
                    }}
                  >
                    <span>{resource.icon}</span>
                    <span style={{ flex: 1 }}>{resource.label}</span>
                    <span style={{ color: '#444' }}>+</span>
                  </button>
                ))}
              </div>
            ))}
            {filteredResources.length === 0 && searchQuery && (
              <div style={{ padding: '20px 16px', color: '#444', fontSize: 12, textAlign: 'center' }}>
                No resources matching "{searchQuery}"
              </div>
            )}
          </div>
        </>
      )}

      {activePanel === 'files' && (
        <div style={{ padding: 16 }}>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#bbb', marginBottom: 12, fontFamily: 'JetBrains Mono' }}>DIR {projectName}/</div>
          {['main' + toolMeta.ext, 'variables' + toolMeta.ext, 'outputs' + toolMeta.ext, '.gitignore'].map(file => (
            <div key={file} style={{ fontSize: 12, color: '#777', padding: '5px 0 5px 12px', fontFamily: 'JetBrains Mono', cursor: 'pointer' }}>FILE {file}</div>
          ))}
          <div style={{ marginTop: 24, padding: 12, background: '#111122', borderRadius: 8, fontSize: 11, color: '#555', lineHeight: 1.6 }}>
            Files sync to:<br /><code style={{ color: toolMeta.color, fontFamily: 'JetBrains Mono' }}>~/{projectName}/</code>
          </div>
        </div>
      )}

      {activePanel === 'suggest' && (
        <div style={S.palScroll}>
          {suggestions.length === 0 ? (
            <div style={{ padding: 20, textAlign: 'center', color: '#555', fontSize: 12 }}>
              Add resources to get smart suggestions based on IaC best practices.
            </div>
          ) : (
            suggestions.map(suggestion => {
              const meta = resources.find(resource => resource.type === suggestion.type);
              return (
                <button
                  key={suggestion.type}
                  style={{ ...S.palItem, flexDirection: 'column', alignItems: 'flex-start', gap: 4, padding: '10px 16px' }}
                  aria-label={`Add suggested resource ${suggestion.label}`}
                  onClick={() => meta && onAddResource(meta)}
                  onMouseEnter={event => { event.currentTarget.style.background = 'var(--bg-elev-2)'; }}
                  onMouseLeave={event => { event.currentTarget.style.background = 'transparent'; }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, width: '100%' }}>
                    <span>{meta?.icon ?? '📦'}</span>
                    <span style={{ flex: 1, fontWeight: 600, color: '#ddd' }}>{suggestion.label}</span>
                    <span style={{ color: suggestion.priority === 1 ? toolMeta.color : suggestion.priority === 2 ? '#888' : '#555', fontSize: 9, fontFamily: 'JetBrains Mono' }}>
                      {suggestion.priority === 1 ? 'NEXT' : suggestion.priority === 2 ? 'RECOMMENDED' : 'OPTIONAL'}
                    </span>
                  </div>
                  <div style={{ fontSize: 11, color: '#666', lineHeight: 1.4, paddingLeft: 28 }}>{suggestion.reason}</div>
                </button>
              );
            })
          )}
        </div>
      )}

      {activePanel === 'guide' && (
        <div style={{ ...S.palScroll, padding: 16 }}>
          <div style={{ fontSize: 14, fontWeight: 700, color: '#ddd', marginBottom: 16 }}>Getting Started</div>
          {[
            { step: '1', title: 'Add a foundation', desc: guideFoundation(tool, provider) },
            { step: '2', title: 'Build networking', desc: tool === 'ansible' ? 'Configure services and users' : 'Add subnets, security groups, and routing' },
            { step: '3', title: 'Add compute', desc: tool === 'ansible' ? 'Deploy application files and templates' : 'Deploy VMs, containers, or serverless functions' },
            { step: '4', title: 'Add data layer', desc: tool === 'ansible' ? 'Configure databases and cron jobs' : 'Add databases, caches, and storage buckets' },
            { step: '5', title: 'Secure & monitor', desc: tool === 'ansible' ? 'Configure firewall and enable services' : 'Add IAM roles, encryption keys, and alarms' },
          ].map(guide => (
            <div key={guide.step} style={{ display: 'flex', gap: 12, marginBottom: 14 }}>
              <div style={{ width: 24, height: 24, borderRadius: '50%', background: toolMeta.color + '22', color: toolMeta.color, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, fontWeight: 700, flexShrink: 0 }}>{guide.step}</div>
              <div>
                <div style={{ fontSize: 12, fontWeight: 600, color: '#bbb' }}>{guide.title}</div>
                <div style={{ fontSize: 11, color: '#666', marginTop: 2 }}>{guide.desc}</div>
              </div>
            </div>
          ))}
          <div style={{ marginTop: 20, padding: 12, background: '#111122', borderRadius: 8, fontSize: 11, color: '#666', lineHeight: 1.6 }}>
            <div style={{ fontWeight: 600, color: '#888', marginBottom: 6 }}>Tips</div>
            <div>Drag the <span style={{ color: toolMeta.color }}>circle port</span> on a node to connect it to another resource.</div>
            <div style={{ marginTop: 4 }}>Use the <span style={{ color: toolMeta.color }}>AI chat</span> below to describe what you need in plain language.</div>
            <div style={{ marginTop: 4 }}>Check the <span style={{ color: toolMeta.color }}>Next</span> tab for smart suggestions based on what's on your canvas.</div>
            <div style={{ marginTop: 4 }}>The code preview on the right updates live as you build.</div>
          </div>
          <button
            style={{ ...S.cmd, background: toolMeta.color + '22', color: toolMeta.color, width: '100%', marginTop: 16, padding: '8px 0' }}
            onClick={() => onActivePanelChange('suggest')}
          >
            View Suggestions →
          </button>
        </div>
      )}
    </aside>
  );
}
