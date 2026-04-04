import { useState, useCallback, useRef } from 'react';

/**
 * useHistory provides undo/redo functionality for any state.
 *
 * Usage:
 *   const { state, set, undo, redo, canUndo, canRedo } = useHistory<MyState>(initialState);
 *
 * The hook maintains a stack of past states (for undo) and future states (for redo).
 * When `set` is called, the current state is pushed to the past stack and future is cleared.
 * Ctrl+Z triggers undo, Ctrl+Shift+Z or Ctrl+Y triggers redo.
 */
export interface HistoryControls<T> {
  state: T;
  set: (newState: T | ((prev: T) => T)) => void;
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
  reset: (newState: T) => void;
  historySize: number;
}

const MAX_HISTORY = 100;

export function useHistory<T>(initialState: T): HistoryControls<T> {
  const [state, setState] = useState<T>(initialState);
  const pastRef = useRef<T[]>([]);
  const futureRef = useRef<T[]>([]);

  const set = useCallback((newState: T | ((prev: T) => T)) => {
    setState(prev => {
      const resolved = typeof newState === 'function'
        ? (newState as (prev: T) => T)(prev)
        : newState;

      // Push current state to past
      pastRef.current = [...pastRef.current.slice(-MAX_HISTORY + 1), prev];
      // Clear future (new action invalidates redo stack)
      futureRef.current = [];

      return resolved;
    });
  }, []);

  const undo = useCallback(() => {
    setState(prev => {
      if (pastRef.current.length === 0) return prev;

      const previous = pastRef.current[pastRef.current.length - 1];
      pastRef.current = pastRef.current.slice(0, -1);
      futureRef.current = [...futureRef.current, prev];

      return previous;
    });
  }, []);

  const redo = useCallback(() => {
    setState(prev => {
      if (futureRef.current.length === 0) return prev;

      const next = futureRef.current[futureRef.current.length - 1];
      futureRef.current = futureRef.current.slice(0, -1);
      pastRef.current = [...pastRef.current, prev];

      return next;
    });
  }, []);

  const reset = useCallback((newState: T) => {
    pastRef.current = [];
    futureRef.current = [];
    setState(newState);
  }, []);

  return {
    state,
    set,
    undo,
    redo,
    canUndo: pastRef.current.length > 0,
    canRedo: futureRef.current.length > 0,
    reset,
    historySize: pastRef.current.length,
  };
}
