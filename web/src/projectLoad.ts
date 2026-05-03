type ProjectStateForLoad = {
  layout?: string;
  resources?: Array<{ file?: string }>;
};

type LayeredForLoad = {
  environments?: string[];
  environmentTools?: Record<string, string>;
} | null;

export const shouldParseResourcesFromDisk = (state: ProjectStateForLoad | null | undefined) => {
  if (!state?.resources || state.resources.length === 0) return true;
  return state.layout === 'layered-v1' && state.resources.some((resource) => !resource.file);
};

const preferredLayeredEnv = (layered: LayeredForLoad, preferredEnv?: string | null) => {
  if (!layered?.environments?.length) return undefined;
  if (preferredEnv && layered.environments.includes(preferredEnv)) return preferredEnv;
  return layered.environments[0];
};

export const toolForEnv = (selectedTool: string, layered: LayeredForLoad, env?: string | null) => {
  if (selectedTool === 'multi' && env) {
    return layered?.environmentTools?.[env] || selectedTool;
  }
  return selectedTool;
};

export const envForTool = (selectedTool: string, layered: LayeredForLoad, preferredEnv?: string | null) => {
  if (selectedTool !== 'pulumi' && selectedTool !== 'multi') return undefined;
  return preferredLayeredEnv(layered, preferredEnv);
};

export const envForResourceLoad = (selectedTool: string, layered: LayeredForLoad) => {
  if (selectedTool === 'pulumi') return preferredLayeredEnv(layered);
  // Hybrid resources are parsed across all environments server-side.
  if (selectedTool === 'multi') return undefined;
  return undefined;
};
