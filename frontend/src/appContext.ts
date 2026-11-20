import { createContext, useContext } from 'react';
import type { Repository } from './types';
import type { useWorkspace } from './useWorkspace';

export interface AppContextValue { repository: Repository; data: ReturnType<typeof useWorkspace> }
export const AppContext = createContext<AppContextValue | null>(null);
export function useApp() {
  const context = useContext(AppContext);
  if (!context) throw new Error('App context is missing.');
  return context;
}
