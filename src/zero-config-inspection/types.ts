import type {
  ConfigDiagnostic,
  ConfigLayerSource,
  ConfigLayerStatus,
  ProviderProfile,
  ZeroConfig,
} from '../config/loader';
import type { ProviderProfileKind } from '../config/types';

export type ZeroConfigLayerSource = ConfigLayerSource;
export type ZeroConfigLayerStatus = ConfigLayerStatus;
export type ZeroConfigValidationError = ConfigDiagnostic;
export type ZeroConfigIssueSeverity = 'warning' | 'error';
export type ZeroConfigProviderSource = 'profile' | 'environment' | 'provider-command' | 'none';

export interface ZeroConfigInspectionOptions {
  now?: () => Date;
  userConfigPath?: string;
  projectConfigPath?: string;
  env?: NodeJS.ProcessEnv;
  cliOverrides?: Partial<ZeroConfig>;
}

export interface ZeroConfigLayerInspection {
  source: ZeroConfigLayerSource;
  status: ZeroConfigLayerStatus;
  present: boolean;
  path?: string;
  config: Partial<ZeroConfig>;
  errors?: ZeroConfigValidationError[];
}

export interface ZeroConfigIssue {
  id: string;
  severity: ZeroConfigIssueSeverity;
  source: ZeroConfigLayerSource | 'provider';
  message: string;
  path?: string;
  fieldPath?: string;
}

export interface ZeroConfigProviderInspection {
  configured: boolean;
  source: ZeroConfigProviderSource;
  provider?: ProviderProfileKind | 'auto';
  profileName?: string;
  baseURL?: string;
  model?: string;
  apiKey?: string;
  commandConfigured?: boolean;
  activeProfile?: ProviderProfile;
}

export interface ZeroConfigInspectionReport {
  generatedAt: string;
  ok: boolean;
  effective: ZeroConfig;
  layers: ZeroConfigLayerInspection[];
  provider: ZeroConfigProviderInspection;
  issues: ZeroConfigIssue[];
}
