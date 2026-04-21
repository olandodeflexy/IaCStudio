import { clsx, type ClassValue } from 'clsx';
import { twMerge } from 'tailwind-merge';

// cn is the shadcn convention: clsx for conditional logic, tailwind-merge
// so later utilities override earlier ones instead of both ending up on
// the element. Use it for every className on new Tailwind components.
export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}
