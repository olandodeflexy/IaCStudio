import { describe, expect, it, beforeEach } from 'vitest';

import { useWorkspaceStore } from './workspace';

// These tests exercise the setters directly on the store — no React
// render needed. If the schema of WorkspaceState drifts (e.g. adding a
// required field), the reset in beforeEach keeps tests deterministic.
describe('workspace store', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({
      activeProject: null,
      focusedPanel: 'canvas',
      sidebarCollapsed: false,
    });
  });

  it('sets active project', () => {
    useWorkspaceStore.getState().setActiveProject('demo');
    expect(useWorkspaceStore.getState().activeProject).toBe('demo');
  });

  it('sets focused panel', () => {
    useWorkspaceStore.getState().setFocusedPanel('policy');
    expect(useWorkspaceStore.getState().focusedPanel).toBe('policy');
  });

  it('toggles the sidebar', () => {
    expect(useWorkspaceStore.getState().sidebarCollapsed).toBe(false);
    useWorkspaceStore.getState().toggleSidebar();
    expect(useWorkspaceStore.getState().sidebarCollapsed).toBe(true);
    useWorkspaceStore.getState().toggleSidebar();
    expect(useWorkspaceStore.getState().sidebarCollapsed).toBe(false);
  });
});
