import { create } from 'zustand';

import type { PanelId } from '../types';

// workspace is the first Zustand slice — it owns cross-panel UI state
// that doesn't belong to any single component (active project, focused
// panel, sidebar collapsed flags). Per-panel state stays local to the
// panel until two panels actually need to share it.
//
// Keeping the slice intentionally small: migrating state out of the
// App.tsx monolith in one go would churn the diff; panels will adopt
// the store as they're extracted in later commits.
interface WorkspaceState {
  activeProject: string | null;
  focusedPanel: PanelId;
  sidebarCollapsed: boolean;

  setActiveProject: (name: string | null) => void;
  setFocusedPanel: (panel: PanelId) => void;
  toggleSidebar: () => void;
}

export const useWorkspaceStore = create<WorkspaceState>((set) => ({
  activeProject: null,
  focusedPanel: 'canvas',
  sidebarCollapsed: false,

  setActiveProject: (name) => set({ activeProject: name }),
  setFocusedPanel: (panel) => set({ focusedPanel: panel }),
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
}));
