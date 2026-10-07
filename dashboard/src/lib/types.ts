export interface DeploymentMeta {
  id: string;
  status: string;
  trigger: string;
  service_name: string;
  service_url: string;
  env_name: string;
  started_at: string;
  finished_at?: string;
}

export interface Project {
  id: string;
  name: string;
  region: string;
  updated_at: string;
  last_deploy?: DeploymentMeta;
}

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  last_used_at: string | null;
  expires_at: string | null;
  created_at: string;
}

export interface BillingSummary {
  plan: string;
  status: string;
  credit_remaining: number;
  monthly_credit: number;
  period_start: string;
  period_end: string;
  total_spend: number;
  usage: Array<{
    resource_type: string;
    total_cost: number;
    total_qty: number;
    unit: string;
  }>;
}

export interface DeploymentStep {
  id: string;
  step: string;
  status: string;
  error?: string;
  started_at?: string;
  finished_at?: string;
}

export interface DeploymentDetail {
  id: string;
  status: string;
  trigger: string;
  error?: string;
  build_log?: string;
  image_uri?: string;
  service_name: string;
  service_url: string;
  service_type: string;
  env_name: string;
  project_name: string;
  started_at: string;
  finished_at?: string;
  steps: DeploymentStep[];
}

export interface DeploymentListItem {
  id: string;
  status: string;
  trigger: string;
  error?: string;
  service_name: string;
  service_url: string;
  env_name: string;
  started_at: string;
  finished_at?: string;
}

export interface ProjectHealth {
  status: "healthy" | "degraded" | "down" | "unknown";
  error_rate_1h: number | null;
  p95_ms: number | null;
  requests_per_min: number | null;
  service_count: number;
  last_deploy: {
    id: string;
    status: string;
    trigger: string;
    started_at: string;
    finished_at: string | null;
  } | null;
}

export interface ProjectMetrics {
  period: string;
  error_rate: Array<{ current: number; avg: number; min: number; max: number; unit: string; trend: string }>;
  p95_ms: Array<{ current: number; avg: number; unit: string; trend: string }>;
  requests: Array<{ current: number; avg: number; unit: string; trend: string }>;
}

export interface RecentError {
  id: string;
  type: string;
  message: string;
  occurred_at: string;
  deployment_id: string;
}

export interface SecretItem {
  name: string;
  is_set: boolean;
}

export interface PurchasedDomain {
  id: string;
  domain: string;
  status: string;
  expires_at: string;
  dns_configured: boolean;
  purchase_price: number;
}

export interface DomainSearchResult {
  domain: string;
  available: boolean;
  price: number;
}

export interface RegistrarDNSRecord {
  id: number;
  type: string;
  host: string;
  value: string;
  ttl?: number;
  priority?: number;
}

export interface DNSListResult {
  domain: string;
  records: RegistrarDNSRecord[];
  total: number;
}

export interface DNSChangeResult {
  domain: string;
  created?: RegistrarDNSRecord[];
  deleted?: number[];
  errors?: string[];
  total: number;
}

export interface SSELogEvent {
  type: "log" | "status" | "done";
  line?: string;
  status?: string;
}
