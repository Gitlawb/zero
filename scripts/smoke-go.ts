import { zeroArtifactName, zeroArtifactPath } from './artifact';

const artifact = Bun.file(zeroArtifactPath);

if (!(await artifact.exists())) {
  console.error(`Go build artifact not found: ${zeroArtifactName}`);
  console.error('Run `bun run build:go` before `bun run smoke:go`.');
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
  const parsedPackage = JSON.parse(packageText);
  if (typeof parsedPackage?.version !== 'string') {
    console.error(`Invalid package.json: version is not a string (${JSON.stringify(parsedPackage?.version)})`);
    process.exit(1);
  }
  expectedVersion = parsedPackage.version;
} catch (error) {
  const message = error instanceof Error ? error.message : String(error);
  console.error(`Failed to parse package.json: ${message}`);
  process.exit(1);
}

const expectedOutput = `zero ${expectedVersion}`;
const actualOutput = stdout.trim();

if (actualOutput !== expectedOutput) {
  console.error(`Expected ${zeroArtifactName} --version to print ${expectedOutput}, got ${actualOutput}`);
  process.exit(1);
}

console.log(`${zeroArtifactName} Go smoke check passed (${expectedVersion})`);
