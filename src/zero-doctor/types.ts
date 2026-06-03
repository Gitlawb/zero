import type { ProviderConfig } from '../config/provider';

export type ZeroDoctorStatus = 'pass' | 'warn' | 'fail';

export interface ZeroDoctorCheck {
  id: string;
  label: string;
  status: ZeroDoctorStatus;
  message: string;
  details?: Record<string, unknown>;
}

export interface ZeroDoctorReport {
  generatedAt: string;
  ok: boolean;
  checks: ZeroDoctorCheck[];
}

export interface ZeroDoctorOptions {
  now?: () => Date;
  bunVersion?: string;
  env?: NodeJS.ProcessEnv;
  userConfigPath?: string;
  projectConfigPath?: string;
  providerConfig?: ProviderConfig | null;
  loadProviderConfig?: () => Promise<ProviderConfig>;
  connectivity?: boolean;
  connectivityTimeoutMs?: number;
  fetchImpl?: typeof fetch;
}
