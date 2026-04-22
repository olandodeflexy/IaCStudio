// Canvas panel — owns the React Flow graph + swimlane layout engine.
//
// The panel shell (toolbar, mode switcher, drag-to-place from the
// resource palette) lands with the App.tsx split in the later commit.
// Today this file re-exports the swimlane pieces so callers already
// have a stable import surface.

export { SwimlaneCanvas, defaultLayeredClassifier } from './SwimlaneCanvas';
export type { SwimlaneCanvasProps } from './SwimlaneCanvas';
export {
  buildSwimlaneLayout,
  cellKey,
  groupResourcesByCell,
  LAYOUT,
} from './swimlaneLayout';
export type { SwimlaneInput, SwimlaneOutput } from './swimlaneLayout';

// Stub preserved so the App.tsx split has a target to drop its
// orchestration into. Intentionally renders null for now — the legacy
// FlowCanvas lives inside App.tsx until the split commit.
export function CanvasPanel() {
  return null;
}
