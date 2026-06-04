#!/usr/bin/env bun

import { dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import { runNpmWrapper } from '../src/npm-wrapper';

const packageRoot = dirname(dirname(fileURLToPath(import.meta.url)));

process.exitCode = await runNpmWrapper({ root: packageRoot });
