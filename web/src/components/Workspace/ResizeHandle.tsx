import type { ResizeState } from '../../app/types';

interface ResizeHandleProps {
  direction: 'col' | 'row';
  panel: ResizeState['panel'];
  activePanel?: string;
  color: string;
  startSize: number;
  onResizeStart: (_state: ResizeState) => void;
}

export function ResizeHandle({
  direction,
  panel,
  activePanel,
  color,
  startSize,
  onResizeStart,
}: ResizeHandleProps) {
  const isRow = direction === 'row';
  return (
    <div
      style={{
        width: isRow ? undefined : 4,
        height: isRow ? 4 : undefined,
        cursor: isRow ? 'row-resize' : 'col-resize',
        background: activePanel === panel ? color + '44' : 'transparent',
        flexShrink: 0,
        transition: 'background 0.15s',
      }}
      onMouseDown={event => onResizeStart({ panel, startPos: isRow ? event.clientY : event.clientX, startSize })}
      onMouseEnter={event => { if (!activePanel) event.currentTarget.style.background = 'var(--border-main)'; }}
      onMouseLeave={event => { if (!activePanel) event.currentTarget.style.background = 'transparent'; }}
    />
  );
}
