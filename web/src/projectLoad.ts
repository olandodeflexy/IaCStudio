type ProjectStateForLoad = {
  layout?: string;
  resources?: Array<{ file?: string }>;
};

type LayeredForLoad = {
  environments?: string[];
} | null;

export const shouldParseResourcesFromDisk = (state: ProjectStateForLoad | null | undefined) => {
  if (!state?.resources || state.resources.length === 0) return true;
  return state.layout === 'layered-v1' && state.resources.some((resource) => !resource.file);
};

export const envForTool = (selectedTool: string, layered: LayeredForLoad) => (
  selectedTool === 'pulumi' ? layered?.environments?.[0] : undefined
);
