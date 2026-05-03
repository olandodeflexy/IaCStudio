// Shared domain types used across the refactored panels. The legacy
// App.tsx keeps its own local shapes for now; as each panel is extracted
// it should import from here and drop its copy.

import type { Resource, ToolInfo, CatalogResource, Suggestion, FileEntry, ImportResult } from './api';

export type { Resource, ToolInfo, CatalogResource, Suggestion, FileEntry, ImportResult };

// A module-level project (layered-v1) pairs an environment with the
// modules it consumes. The backend returns this in the project state
// blob when the project was scaffolded with the layered layout.
export interface LayeredProject {
  layout: 'layered-v1' | 'flat';
  environments: string[];
  environmentTools?: Record<string, string>;
  modules: LayeredModule[];
}

export interface LayeredModule {
  name: string;
  path: string;
  source?: string;
  // which environments consume this module — used for swimlane layout
  environments: string[];
}

// Connection edge between resource nodes on the freeform canvas. The
// canvas module exports a richer Edge type; this is the shape persisted
// to the backend state file.
export interface PersistedEdge {
  id: string;
  from: string;
  to: string;
  field: string;
}

// Panels that live in the main workspace area. The UI store tracks
// which one is focused so keyboard shortcuts can route appropriately.
export type PanelId =
  | 'canvas'
  | 'inspector'
  | 'chat'
  | 'terminal'
  | 'policy'
  | 'scan'
  | 'registry';

// Editor language hints — Monaco needs these to pick a tokenizer.
export type EditorLanguage = 'hcl' | 'rego' | 'sentinel' | 'json' | 'yaml' | 'plaintext';
