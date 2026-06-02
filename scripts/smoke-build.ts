import { zeroArtifactName, zeroArtifactPath } from './artifact';

const artifact = Bun.file(zeroArtifactPath);

if (!(await artifact.exists())) {
  console.error(`Build artifact not found: ${zeroArtifactName}`);
  process.exit(1);
}

const child = Bun.spawn([zeroArtifactPath, '--version'], {
  stderr: 'pipe',
  stdout: 'pipe',
});

const [exitCode, stdout, stderr, packageText] = await Promise.all([
  child.exited,
  new Response(child.stdout).text(),
  new Response(child.stderr).text(),
  Bun.file('package.json').text(),
]);

if (exitCode !== 0) {
  console.error(stderr.trim() || `${zeroArtifactName} --version exited with ${exitCode}`);
  process.exit(exitCode);
}

let expectedVersion: string;

try {
  expectedVersion = JSON.parse(packageText).version;
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`Failed to parse package.json: ${message}`);
  process.exit(1);
}

const actualVersion = stdout.trim();

if (actualVersion !== expectedVersion) {
  console.error(`Expected ${zeroArtifactName} --version to print ${expectedVersion}, got ${actualVersion}`);
  process.exit(1);
}

console.log(`${zeroArtifactName} smoke check passed (${actualVersion})`);
