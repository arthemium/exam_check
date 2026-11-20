import type { Repository } from '../types';
import { createApiRepository } from './api';
import { createDemoRepository } from './demo';

export function createRepository(mode: 'demo' | 'api'): Repository {
  return mode === 'api' ? createApiRepository() : createDemoRepository();
}
