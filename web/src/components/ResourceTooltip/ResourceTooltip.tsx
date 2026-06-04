import type { CatalogResource } from '../../api';

export interface ResourceTooltipProps {
  resource: CatalogResource;
  position: { x: number; y: number };
  toolColor: string;
}

export function ResourceTooltip({ resource, position, toolColor }: ResourceTooltipProps) {
  return (
    <div style={{
      position: 'fixed', left: position.x, top: position.y,
      background: 'var(--bg-elev-2)', border: '1px solid var(--border-main)', borderRadius: 10,
      padding: '12px 16px', zIndex: 1000, maxWidth: 300, minWidth: 220,
      boxShadow: '0 8px 24px rgba(0,0,0,0.5)', pointerEvents: 'none',
      fontFamily: 'DM Sans',
    }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
        <span style={{ fontSize: 20 }}>{resource.icon}</span>
        <div>
          <div style={{ fontSize: 13, fontWeight: 600, color: '#e0e0f0' }}>{resource.label}</div>
          <div style={{ fontSize: 10, color: '#666', fontFamily: 'JetBrains Mono' }}>{resource.type}</div>
        </div>
      </div>
      {resource.provider && (
        <div style={{ fontSize: 10, color: '#888', marginBottom: 6 }}>
          Provider: <span style={{ color: toolColor }}>{resource.provider}</span>
        </div>
      )}
      {resource.fields && resource.fields.length > 0 && (
        <div style={{ marginBottom: 6 }}>
          <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Fields</div>
          {resource.fields.slice(0, 6).map(field => (
            <div key={field.name} style={{ fontSize: 11, color: '#999', display: 'flex', gap: 4, lineHeight: 1.6, fontFamily: 'JetBrains Mono' }}>
              <span style={{ color: field.required ? '#ef4444' : '#555' }}>{field.required ? '*' : ' '}</span>
              <span style={{ color: '#aaa' }}>{field.name}</span>
              <span style={{ color: '#555', marginLeft: 'auto' }}>{field.type}</span>
            </div>
          ))}
          {resource.fields.length > 6 && (
            <div style={{ fontSize: 10, color: '#444', marginTop: 2 }}>+{resource.fields.length - 6} more</div>
          )}
        </div>
      )}
      {resource.connects_via && Object.keys(resource.connects_via).length > 0 && (
        <div>
          <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Connects To</div>
          {Object.entries(resource.connects_via).map(([field, target]) => (
            <div key={field} style={{ fontSize: 11, color: '#777', fontFamily: 'JetBrains Mono', lineHeight: 1.6 }}>
              <span style={{ color: toolColor }}>{field}</span> {'->'} <span style={{ color: '#aaa' }}>{target}</span>
            </div>
          ))}
        </div>
      )}
      {resource.defaults && Object.keys(resource.defaults).length > 0 && (
        <div style={{ marginTop: 6, paddingTop: 6, borderTop: '1px solid var(--border-soft)' }}>
          <div style={{ fontSize: 9, color: '#555', textTransform: 'uppercase', letterSpacing: 1, marginBottom: 4, fontFamily: 'JetBrains Mono' }}>Defaults</div>
          {Object.entries(resource.defaults).slice(0, 4).map(([key, value]) => (
            <div key={key} style={{ fontSize: 10, color: '#666', fontFamily: 'JetBrains Mono', lineHeight: 1.5 }}>
              {key}: <span style={{ color: '#888' }}>{String(value)}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
