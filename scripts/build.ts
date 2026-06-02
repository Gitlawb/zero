import { $ } from 'bun';
import { zeroArtifactName, zeroArtifactPath } from './artifact';

await $`bun build src/index.ts --compile --outfile ${zeroArtifactPath}`;
console.log(`Built ${zeroArtifactName}`);
