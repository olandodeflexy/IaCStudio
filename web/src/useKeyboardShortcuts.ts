import { useEffect, useCallback } from 'react';

export interface ShortcutMap {
  [key: string]: () => void;
}

/**
 * useKeyboardShortcuts registers global keyboard shortcuts.
 *
 * Key format: "ctrl+z", "ctrl+shift+z", "delete", "escape", "ctrl+s", "ctrl+d"
 *
 * Usage:
 *   useKeyboardShortcuts({
 *     'ctrl+z': undo,
 *     'ctrl+shift+z': redo,
 *     'ctrl+y': redo,
 *     'delete': deleteSelected,
 *     'backspace': deleteSelected,
 *     'escape': clearSelection,
 *     'ctrl+s': save,
 *     'ctrl+a': selectAll,
 *     'ctrl+d': duplicate,
 *   });
 */
export function useKeyboardShortcuts(shortcuts: ShortcutMap) {
  const handler = useCallback((e: KeyboardEvent) => {
    // Don't intercept when typing in inputs
    const target = e.target as HTMLElement;
    if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) {
      // Allow Escape in inputs
      if (e.key !== 'Escape') return;
    }

    const parts: string[] = [];
    if (e.ctrlKey || e.metaKey) parts.push('ctrl');
    if (e.shiftKey) parts.push('shift');
    if (e.altKey) parts.push('alt');

    const key = e.key.toLowerCase();
    if (!['control', 'shift', 'alt', 'meta'].includes(key)) {
      parts.push(key);
    }

    const combo = parts.join('+');

    if (shortcuts[combo]) {
      e.preventDefault();
      e.stopPropagation();
      shortcuts[combo]();
    }
  }, [shortcuts]);

  useEffect(() => {
    window.addEventListener('keydown', handler);
    return () => window.removeEventListener('keydown', handler);
  }, [handler]);
}
