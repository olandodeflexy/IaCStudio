import type { Edge } from '../legacy';
import type { LayeredModule, LayeredProject } from '../types';

export const extractLayoutMeta = (state: any) => {
  if (!state) return null;
  const meta: Record<string, any> = {};
  for (const key of ['layout', 'blueprint', 'project_name', 'cloud', 'environments', 'environment_tools', 'modules', 'tags', 'drift']) {
    if (state[key] !== undefined) meta[key] = state[key];
  }
  return Object.keys(meta).length > 0 ? meta : null;
};

export const normalizeLayeredProject = (state: any): LayeredProject | null => {
  if (state?.layout !== 'layered-v1') return null;
  const environments = Array.isArray(state.environments)
    ? state.environments.filter((env: unknown): env is string => typeof env === 'string' && env.length > 0)
    : [];
  if (environments.length === 0) return null;
  const rawEnvironmentTools = state.environment_tools;
  let environmentTools: Record<string, string> | undefined;
  if (
    rawEnvironmentTools &&
    typeof rawEnvironmentTools === 'object' &&
    !Array.isArray(rawEnvironmentTools) &&
    [Object.prototype, null].includes(Object.getPrototypeOf(rawEnvironmentTools))
  ) {
    const normalizedEnvironmentTools = Object.create(null) as Record<string, string>;
    for (const [env, envTool] of Object.entries(rawEnvironmentTools)) {
      if (environments.includes(env) && typeof envTool === 'string' && envTool.length > 0) {
        normalizedEnvironmentTools[env] = envTool;
      }
    }
    if (Object.keys(normalizedEnvironmentTools).length > 0) {
      environmentTools = normalizedEnvironmentTools;
    }
  }

  const rawModules = Array.isArray(state.modules) ? state.modules : [];
  const modules: LayeredModule[] = rawModules
    .map((mod: unknown) => {
      if (typeof mod === 'string') {
        return { name: mod, path: `modules/${mod}`, environments };
      }
      if (mod && typeof mod === 'object') {
        const candidate = mod as Partial<LayeredModule>;
        if (!candidate.name) return null;
        return {
          name: candidate.name,
          path: candidate.path || `modules/${candidate.name}`,
          source: candidate.source,
          environments: Array.isArray(candidate.environments) && candidate.environments.length > 0
            ? candidate.environments
            : environments,
        };
      }
      return null;
    })
    .filter((mod): mod is LayeredModule => Boolean(mod?.name));

  if (!modules.some((mod) => mod.name === 'root')) {
    modules.unshift({ name: 'root', path: 'environments', environments });
  }

  return { layout: 'layered-v1', environments, environmentTools, modules };
};

export const resourceEnv = (resource: { file?: string }) => {
  if (!resource.file) return null;
  const parts = resource.file.replace(/\\/g, '/').split('/');
  const envIdx = parts.indexOf('environments');
  return envIdx >= 0 && parts.length > envIdx + 1 ? parts[envIdx + 1] : null;
};

export const resourcesForEnv = <T extends { id: string; file?: string }>(resources: T[], env?: string) => {
  if (!env) return resources;
  return resources.filter(resource => {
    const envFromFile = resourceEnv(resource);
    return envFromFile === env;
  });
};

export const edgesForResources = (allEdges: Edge[], resources: { id: string }[]) => {
  const ids = new Set(resources.map(resource => resource.id));
  return allEdges.filter(edge => ids.has(edge.from) && ids.has(edge.to));
};
