import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { ProjectLauncher, type ProjectLauncherProps } from './ProjectLauncher';

function baseProps(): ProjectLauncherProps {
  return {
    savedProjects: [],
    detectedTools: [],
    projectName: 'demo-project',
    showImportWizard: false,
    importTab: 'browse',
    browsePath: '',
    browseParent: '',
    browseEntries: [],
    importPreview: null,
    importLoading: false,
    topologyDesc: '',
    topologyProvider: 'aws',
    visionImages: [],
    visionError: null,
    catalogResources: [],
    onProjectNameChange: vi.fn(),
    onCreateProject: vi.fn(),
    onOpenProject: vi.fn(),
    onRevealProject: vi.fn(),
    onDeleteProject: vi.fn(),
    onStartImportBrowse: vi.fn(),
    onStartTopology: vi.fn(),
    onImportTabChange: vi.fn(),
    onBrowseLoaded: vi.fn(),
    onImportPreviewChange: vi.fn(),
    onImportLoadingChange: vi.fn(),
    onTopologyDescChange: vi.fn(),
    onTopologyProviderChange: vi.fn(),
    onVisionImagesChange: vi.fn(),
    onVisionErrorChange: vi.fn(),
    onGenerateTopology: vi.fn(),
    onImportToCanvas: vi.fn(),
    onCloseImportWizard: vi.fn(),
  };
}

function renderLauncher(overrides: Partial<ProjectLauncherProps> = {}) {
  const props = { ...baseProps(), ...overrides };
  render(<ProjectLauncher {...props} />);
  return props;
}

describe('ProjectLauncher', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('dispatches new-project and import actions', () => {
    const props = renderLauncher({
      detectedTools: [{ name: 'Terraform', binary: 'terraform', version: 'v1.9.0', available: true }],
    });

    expect(screen.getByRole('heading', { name: 'Choose your IaC tool' })).toBeInTheDocument();
    expect(screen.getByText(/v1.9.0/)).toBeInTheDocument();

    fireEvent.change(screen.getByRole('textbox', { name: 'Project name' }), { target: { value: 'renamed' } });
    fireEvent.click(screen.getByRole('button', { name: /Terraform/i }));
    fireEvent.click(screen.getByRole('button', { name: 'Import Existing Project' }));
    fireEvent.click(screen.getByRole('button', { name: 'Build with AI' }));

    expect(props.onProjectNameChange).toHaveBeenCalledWith('renamed');
    expect(props.onCreateProject).toHaveBeenCalledWith('terraform');
    expect(props.onStartImportBrowse).toHaveBeenCalled();
    expect(props.onStartTopology).toHaveBeenCalled();
  });

  it('renders saved projects and dispatches project actions', () => {
    const savedProject = {
      name: 'existing-project',
      tool: 'terraform',
      resources: [{ id: 'vpc' }],
      updated_at: '2026-05-01T00:00:00Z',
    };
    const confirm = vi.fn(() => true);
    vi.stubGlobal('confirm', confirm);
    const props = renderLauncher({ savedProjects: [savedProject] });

    expect(screen.getByRole('heading', { name: 'New Project' })).toBeInTheDocument();
    expect(screen.getByText('Recent Projects')).toBeInTheDocument();
    expect(screen.getByText(/1 resource/)).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: 'Open project existing-project' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open files for existing-project' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete project existing-project' }));

    expect(props.onOpenProject).toHaveBeenCalledWith(savedProject);
    expect(props.onRevealProject).toHaveBeenCalledWith('existing-project');
    expect(confirm).toHaveBeenCalled();
    expect(props.onDeleteProject).toHaveBeenCalledWith('existing-project');
  });

  it('does not delete a saved project when confirmation is cancelled', () => {
    vi.stubGlobal('confirm', vi.fn(() => false));
    const props = renderLauncher({
      savedProjects: [{ name: 'existing-project', tool: 'terraform' }],
    });

    fireEvent.click(screen.getByRole('button', { name: 'Delete project existing-project' }));

    expect(props.onDeleteProject).not.toHaveBeenCalled();
  });
});
