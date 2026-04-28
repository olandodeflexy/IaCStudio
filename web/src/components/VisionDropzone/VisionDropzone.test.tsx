import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

import { MAX_VISION_IMAGE_BYTES, VisionDropzone, validateVisionFiles } from './VisionDropzone';

function imageFile(name = 'diagram.png', type = 'image/png') {
  return new File(['image-bytes'], name, { type });
}

describe('VisionDropzone', () => {
  beforeEach(() => {
    Object.defineProperty(URL, 'createObjectURL', {
      value: vi.fn(() => 'blob:preview'),
      configurable: true,
    });
    Object.defineProperty(URL, 'revokeObjectURL', {
      value: vi.fn(),
      configurable: true,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('accepts dropped images and renders a preview', () => {
    const onFilesChange = vi.fn();
    const { rerender } = render(<VisionDropzone files={[]} onFilesChange={onFilesChange} />);

    const file = imageFile();
    fireEvent.drop(screen.getByRole('button', { name: /upload architecture/i }), {
      dataTransfer: { files: [file] },
    });

    expect(onFilesChange).toHaveBeenCalledWith([file]);
    rerender(<VisionDropzone files={[file]} onFilesChange={onFilesChange} />);
    expect(screen.getByRole('img', { name: file.name })).toBeInTheDocument();
  });

  it('accepts pasted images', () => {
    const onFilesChange = vi.fn();
    render(<VisionDropzone files={[]} onFilesChange={onFilesChange} />);

    const file = imageFile('pasted.webp', 'image/webp');
    fireEvent.paste(screen.getByRole('button', { name: /upload architecture/i }), {
      clipboardData: { files: [file] },
    });

    expect(onFilesChange).toHaveBeenCalledWith([file]);
  });

  it('rejects unsupported file types client-side', () => {
    const onFilesChange = vi.fn();
    const onError = vi.fn();
    render(<VisionDropzone files={[]} onFilesChange={onFilesChange} onError={onError} />);

    fireEvent.drop(screen.getByRole('button', { name: /upload architecture/i }), {
      dataTransfer: { files: [new File(['x'], 'diagram.svg', { type: 'image/svg+xml' })] },
    });

    expect(onFilesChange).not.toHaveBeenCalled();
    expect(onError).toHaveBeenCalledWith(expect.stringContaining('not supported'));
  });

  it('validates count and size limits', () => {
    const oversized = imageFile('big.png');
    Object.defineProperty(oversized, 'size', { value: MAX_VISION_IMAGE_BYTES + 1 });

    expect(validateVisionFiles([oversized])).toContain('too large');
    expect(validateVisionFiles(Array.from({ length: 6 }, (_, i) => imageFile(`d${i}.png`)))).toContain('Upload up to 5 images');
  });
});
