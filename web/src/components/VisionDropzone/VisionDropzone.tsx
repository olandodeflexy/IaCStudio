import { useCallback, useEffect, useRef, useState } from 'react';
import { Image as ImageIcon, Upload, X } from 'lucide-react';

export const MAX_VISION_IMAGE_BYTES = 8 * 1024 * 1024;
export const MAX_VISION_IMAGE_COUNT = 5;
export const MAX_VISION_REQUEST_BYTES = 20 * 1024 * 1024;
export const ACCEPTED_VISION_IMAGE_TYPES = new Set([
  'image/png',
  'image/jpeg',
  'image/jpg',
  'image/webp',
  'image/gif',
]);

export interface VisionDropzoneProps {
  files: File[];
  onFilesChange: (_files: File[]) => void;
  onError?: (_message: string | null) => void;
  error?: string | null;
  disabled?: boolean;
}

export function validateVisionFiles(files: File[], existingFiles: File[] = []): string | null {
  if (files.length === 0) return null;
  if (existingFiles.length + files.length > MAX_VISION_IMAGE_COUNT) {
    return `Upload up to ${MAX_VISION_IMAGE_COUNT} images.`;
  }
  const badType = files.find(file => !ACCEPTED_VISION_IMAGE_TYPES.has(file.type.toLowerCase()));
  if (badType) {
    return `${badType.name} is not supported. Use PNG, JPEG, WEBP, or GIF.`;
  }
  const oversize = files.find(file => file.size > MAX_VISION_IMAGE_BYTES);
  if (oversize) {
    return `${oversize.name} is too large. Maximum size is 8MB.`;
  }
  const totalBytes = [...existingFiles, ...files].reduce((sum, file) => sum + file.size, 0);
  if (totalBytes > MAX_VISION_REQUEST_BYTES) {
    return 'Upload size is too large. Maximum total size is 20MB.';
  }
  return null;
}

function formatBytes(size: number) {
  if (size < 1024 * 1024) return `${Math.max(1, Math.round(size / 1024))}KB`;
  return `${(size / 1024 / 1024).toFixed(1)}MB`;
}

export function VisionDropzone({
  files,
  onFilesChange,
  onError,
  error,
  disabled = false,
}: VisionDropzoneProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const [previews, setPreviews] = useState<{ file: File; url: string }[]>([]);

  useEffect(() => {
    const next = files.map(file => ({ file, url: URL.createObjectURL(file) }));
    setPreviews(next);
    return () => {
      next.forEach(preview => URL.revokeObjectURL(preview.url));
    };
  }, [files]);

  const addFiles = useCallback((incoming: File[]) => {
    if (disabled || incoming.length === 0) return;
    const validationError = validateVisionFiles(incoming, files);
    if (validationError) {
      onError?.(validationError);
      return;
    }
    onFilesChange([...files, ...incoming]);
    onError?.(null);
  }, [disabled, files, onError, onFilesChange]);

  useEffect(() => {
    if (disabled) return;
    const handlePaste = (event: ClipboardEvent) => {
      const pasted = Array.from(event.clipboardData?.files || []);
      if (pasted.length === 0) return;
      event.preventDefault();
      addFiles(pasted);
    };
    window.addEventListener('paste', handlePaste);
    return () => window.removeEventListener('paste', handlePaste);
  }, [addFiles, disabled]);

  const removeFile = (target: File) => {
    onFilesChange(files.filter(file => file !== target));
    onError?.(null);
  };

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div
        role="button"
        tabIndex={disabled ? -1 : 0}
        aria-label="Upload architecture diagram images"
        onClick={() => !disabled && inputRef.current?.click()}
        onKeyDown={event => {
          if (!disabled && (event.key === 'Enter' || event.key === ' ')) {
            event.preventDefault();
            inputRef.current?.click();
          }
        }}
        onDragOver={event => {
          event.preventDefault();
          if (!disabled) setDragging(true);
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={event => {
          event.preventDefault();
          setDragging(false);
          addFiles(Array.from(event.dataTransfer.files));
        }}
        style={{
          minHeight: 128,
          border: `1px dashed ${dragging ? 'var(--accent-action)' : 'var(--border-main)'}`,
          borderRadius: 8,
          background: dragging ? 'rgba(45, 212, 191, 0.08)' : 'var(--bg-elev-1)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          padding: 16,
          cursor: disabled ? 'not-allowed' : 'pointer',
          opacity: disabled ? 0.6 : 1,
        }}
      >
        <input
          ref={inputRef}
          type="file"
          accept="image/png,image/jpeg,image/jpg,image/webp,image/gif"
          multiple
          disabled={disabled}
          aria-label="Choose diagram images"
          style={{ display: 'none' }}
          onChange={event => {
            addFiles(Array.from(event.target.files || []));
            event.currentTarget.value = '';
          }}
        />
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8, textAlign: 'center' }}>
          <Upload size={22} color="var(--accent-action)" />
          <div style={{ fontSize: 13, color: 'var(--text-main)', fontWeight: 600 }}>Drop or paste architecture diagrams</div>
          <div style={{ fontSize: 11, color: 'var(--text-muted)' }}>PNG, JPEG, WEBP, or GIF. Up to 5 images, 8MB each, 20MB total.</div>
        </div>
      </div>

      {error && (
        <div style={{ fontSize: 11, color: '#ef4444', fontFamily: 'JetBrains Mono' }}>{error}</div>
      )}

      {previews.length > 0 && (
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(112px, 1fr))', gap: 8 }}>
          {previews.map(({ file, url }, index) => (
            <div key={`${file.name}-${file.lastModified}-${file.size}-${index}`} style={{ position: 'relative', border: '1px solid var(--border-soft)', borderRadius: 8, overflow: 'hidden', background: 'var(--bg-elev-1)' }}>
              <div style={{ aspectRatio: '4 / 3', display: 'flex', alignItems: 'center', justifyContent: 'center', background: '#0e1412' }}>
                <img src={url} alt={file.name} style={{ width: '100%', height: '100%', objectFit: 'cover' }} />
              </div>
              <div style={{ display: 'flex', gap: 6, alignItems: 'center', padding: '6px 8px' }}>
                <ImageIcon size={12} color="var(--text-muted)" />
                <div style={{ minWidth: 0, flex: 1 }}>
                  <div style={{ fontSize: 10, color: 'var(--text-main)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>{file.name}</div>
                  <div style={{ fontSize: 9, color: 'var(--text-muted)', fontFamily: 'JetBrains Mono' }}>{formatBytes(file.size)}</div>
                </div>
                <button
                  type="button"
                  aria-label={`Remove ${file.name}`}
                  onClick={event => {
                    event.stopPropagation();
                    removeFile(file);
                  }}
                  style={{ border: 0, background: 'transparent', color: 'var(--text-muted)', cursor: 'pointer', padding: 2 }}
                >
                  <X size={14} />
                </button>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
