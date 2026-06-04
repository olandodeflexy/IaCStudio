import type { Resource } from '../api';

export type AppResource = Resource & {
  x: number;
  y: number;
  icon: string;
  label: string;
};

export type ChatMessage = {
  role: string;
  text: string;
  id?: string;
};

export type HistorySetter<T> = (_newState: T | ((_prev: T) => T)) => void;

export type ResizeState = {
  panel: 'sidebar' | 'right' | 'bottom';
  startPos: number;
  startSize: number;
};
