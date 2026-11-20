import { cp, mkdir, access } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

const build = fileURLToPath(new URL('../dist/', import.meta.url));
const destination = fileURLToPath(new URL('../../backend/static/', import.meta.url));
await access(new URL('../dist/index.html', import.meta.url));
await mkdir(destination, { recursive: true });
await cp(build, destination, { recursive: true });
console.log(`Frontend build copied to ${destination}`);
