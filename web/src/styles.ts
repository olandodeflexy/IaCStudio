import type { CSSProperties } from 'react';

// Shared inline-style object used by the legacy App.tsx shell and the
// panels being extracted out of it. Kept as-is (rather than migrated to
// Tailwind classes) so the split commit is a move, not a rewrite —
// visual parity is easier to verify. Once every panel is extracted we
// can replace these with Tailwind utilities one panel at a time.
export const S: Record<string, CSSProperties> = {
  selectScreen: { width: '100vw', height: '100vh', display: 'flex', alignItems: 'flex-start', justifyContent: 'center', background: 'var(--bg-app)', position: 'relative', overflowY: 'auto' as const },
  selectBg: {
    position: 'fixed',
    inset: 0,
    background: 'var(--bg-app)',
    pointerEvents: 'none' as const,
  },
  selectContent: { position: 'relative', zIndex: 1, textAlign: 'center', padding: '40px 40px 60px', marginTop: 40 },
  logo: { display: 'flex', alignItems: 'center', justifyContent: 'center', gap: 10, marginBottom: 32 },
  logoText: { fontSize: 22, fontWeight: 700, color: 'var(--text-main)', fontFamily: 'JetBrains Mono', letterSpacing: 1 },
  title: { fontSize: 38, fontWeight: 700, color: 'var(--text-main)', margin: '0 0 12px', letterSpacing: -0.4, fontFamily: 'Space Grotesk' },
  subtitle: { fontSize: 16, color: 'var(--text-muted)', margin: '0 0 40px' },
  cardGrid: { display: 'flex', gap: 20, justifyContent: 'center', marginBottom: 48 },
  card: { display: 'flex', flexDirection: 'column' as const, alignItems: 'center', gap: 12, padding: '32px 40px', background: 'var(--bg-elev-1)', border: '1.5px solid', borderRadius: 16, cursor: 'pointer', transition: 'all 0.3s', fontFamily: 'DM Sans' },
  features: { display: 'flex', gap: 24, justifyContent: 'center', flexWrap: 'wrap' as const },

  app: { width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' as const, background: 'var(--bg-app)', overflow: 'hidden', position: 'relative' as const },
  notification: { position: 'absolute' as const, top: 60, left: '50%', transform: 'translateX(-50%)', zIndex: 100, background: 'var(--bg-elev-2)', border: '1px solid var(--border-main)', borderRadius: 8, padding: '8px 20px', fontSize: 12, color: 'var(--text-main)', fontFamily: 'JetBrains Mono' },
  header: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0 16px', height: 52, borderBottom: '1px solid', flexShrink: 0, background: 'rgba(21, 25, 24, 0.8)' },
  hLeft: { display: 'flex', alignItems: 'center', gap: 12 },
  hRight: { display: 'flex', alignItems: 'center', gap: 8 },
  backBtn: { background: 'none', border: '1px solid var(--border-main)', color: 'var(--text-muted)', borderRadius: 8, padding: '4px 10px', cursor: 'pointer', fontSize: 16, fontFamily: 'DM Sans' },
  badge: { padding: '4px 12px', borderRadius: 20, fontSize: 13, fontWeight: 600, fontFamily: 'JetBrains Mono' },
  projInput: { background: 'transparent', border: 'none', color: 'var(--text-main)', fontSize: 14, fontFamily: 'JetBrains Mono', fontWeight: 500, outline: 'none', width: 180 },
  count: { fontSize: 12, color: 'var(--text-muted)', fontFamily: 'JetBrains Mono', marginRight: 8 },
  cmd: { border: 'none', borderRadius: 8, padding: '6px 14px', cursor: 'pointer', fontSize: 12, fontWeight: 600, fontFamily: 'JetBrains Mono', transition: 'all 0.2s' },

  main: { display: 'flex', flex: 1, minHeight: 0 },
  sidebar: { width: 240, borderRight: '1px solid var(--border-soft)', display: 'flex', flexDirection: 'column' as const, background: 'var(--bg-elev-1)', flexShrink: 0 },
  tabs: { display: 'flex', borderBottom: '1px solid var(--border-soft)' },
  tab: { flex: 1, padding: '10px 0', background: 'none', border: 'none', borderBottom: '2px solid transparent', color: 'var(--text-muted)', cursor: 'pointer', fontSize: 12, fontWeight: 600, letterSpacing: 0.5, textTransform: 'uppercase' as const, transition: 'all 0.2s', fontFamily: 'DM Sans' },
  palScroll: { flex: 1, overflowY: 'auto' as const, padding: '8px 0' },
  catTitle: { fontSize: 10, fontWeight: 700, color: '#444', textTransform: 'uppercase' as const, letterSpacing: 1.2, padding: '8px 16px 4px', fontFamily: 'JetBrains Mono' },
  palItem: { display: 'flex', alignItems: 'center', gap: 10, width: '100%', padding: '8px 16px', background: 'transparent', border: 'none', color: '#bbb', cursor: 'pointer', fontSize: 13, fontFamily: 'DM Sans', textAlign: 'left' as const, transition: 'background 0.15s' },

  canvas: { flex: 1, position: 'relative' as const, overflow: 'hidden', cursor: 'default' },
  grid: { position: 'absolute' as const, inset: 0, backgroundImage: 'radial-gradient(circle, rgba(217, 226, 220, 0.03) 1px, transparent 1px)', backgroundSize: '24px 24px', opacity: 0.5 },
  empty: { position: 'absolute' as const, top: '50%', left: '50%', transform: 'translate(-50%, -50%)', textAlign: 'center' as const, color: '#555' },
  node: { position: 'absolute' as const, width: 180, background: 'var(--bg-elev-2)', border: '1.5px solid', borderRadius: 12, cursor: 'grab', userSelect: 'none' as const, transition: 'border-color 0.2s, box-shadow 0.2s' },
  nodeHead: { display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px 4px' },
  nodeDel: { background: 'none', border: 'none', color: '#555', fontSize: 18, cursor: 'pointer', padding: 0, lineHeight: 1 },

  right: { width: 300, borderLeft: '1px solid var(--border-soft)', display: 'flex', flexDirection: 'column' as const, background: 'var(--bg-elev-1)', flexShrink: 0 },
  props: { borderBottom: '1px solid var(--border-soft)', padding: 16, maxHeight: '40%', overflowY: 'auto' as const },
  field: { marginBottom: 10 },
  flabel: { fontSize: 10, color: 'var(--text-muted)', display: 'block', marginBottom: 4, fontFamily: 'JetBrains Mono', textTransform: 'uppercase' as const, letterSpacing: 0.5 },
  finput: { width: '100%', padding: '6px 10px', background: 'var(--bg-elev-2)', border: '1px solid var(--border-main)', borderRadius: 6, color: 'var(--text-main)', fontSize: 12, fontFamily: 'JetBrains Mono', outline: 'none', boxSizing: 'border-box' as const },
  ftoggle: { padding: '5px 12px', borderRadius: 6, border: '1px solid var(--border-main)', cursor: 'pointer', fontSize: 12, fontFamily: 'JetBrains Mono', fontWeight: 500, width: '100%' },
  codePanel: { flex: 1, display: 'flex', flexDirection: 'column' as const, minHeight: 0 },
  codeHead: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '10px 16px', fontSize: 12, fontWeight: 600, color: 'var(--text-muted)', borderBottom: '1px solid var(--border-soft)', fontFamily: 'JetBrains Mono' },
  copyBtn: { background: 'none', border: 'none', fontSize: 11, cursor: 'pointer', fontFamily: 'JetBrains Mono', fontWeight: 600 },
  codePre: { flex: 1, minHeight: 0, display: 'flex' as const, padding: 12 },

  bottom: { display: 'flex', height: 220, borderTop: '1px solid var(--border-soft)', flexShrink: 0 },
  chat: { flex: 1, display: 'flex', flexDirection: 'column' as const, borderRight: '1px solid var(--border-soft)' },
  chatHead: { display: 'flex', alignItems: 'center', gap: 8, padding: '8px 16px', fontSize: 12, fontWeight: 600, color: 'var(--text-main)', borderBottom: '1px solid var(--border-soft)', background: 'var(--bg-elev-1)' },
  chatBadge: { fontSize: 9, background: 'var(--bg-elev-3)', padding: '2px 8px', borderRadius: 10, color: 'var(--text-muted)', marginLeft: 'auto', fontFamily: 'JetBrains Mono' },
  chatMsgs: { flex: 1, overflowY: 'auto' as const, padding: '8px 16px' },
  chatInputRow: { display: 'flex', gap: 8, padding: '8px 16px', borderTop: '1px solid var(--border-soft)', background: 'var(--bg-elev-1)' },
  chatInput: { flex: 1, padding: '8px 12px', background: 'var(--bg-elev-2)', border: '1px solid var(--border-main)', borderRadius: 8, color: 'var(--text-main)', fontSize: 13, fontFamily: 'DM Sans', outline: 'none' },
  chatSend: { width: 36, height: 36, borderRadius: 8, border: 'none', color: '#000', fontSize: 16, fontWeight: 700, cursor: 'pointer' },

  term: { width: 380, display: 'flex', flexDirection: 'column' as const, background: '#09090f', flexShrink: 0 },
  termHead: { display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '8px 16px', fontSize: 12, fontWeight: 600, color: 'var(--text-muted)', borderBottom: '1px solid var(--border-soft)' },
  termClear: { background: 'none', border: 'none', color: '#444', fontSize: 11, cursor: 'pointer', fontFamily: 'JetBrains Mono' },
  termContent: { flex: 1, padding: '8px 16px', fontSize: 11, fontFamily: 'JetBrains Mono', lineHeight: 1.8, overflowY: 'auto' as const },
};
