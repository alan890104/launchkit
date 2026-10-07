package deploy

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const (
	// DoS prevention limits
	maxServicesPerPlan     = 100 // Max services in a single deployment
	maxEnvVarsPerService   = 100 // Max env vars per service
	maxBuildArgsPerService = 50  // Max build args per service
	maxResourcesPerPlan    = 20  // Max resources in a single deployment
	maxServiceNameLength   = 63  // Kubernetes/DNS label length limit
	maxDomainLength        = 253 // RFC 1035 total domain name limit
)

var (
	envKeyRe      = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)
	serviceNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	// domainLabelRe matches a single DNS label (between dots).
	domainLabelRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?$`)
	// projectIDRe enforces that project IDs are UUIDs (with or without hyphens).
	// Non-UUID project IDs allow colon-injection into URNs (resource_states.urn).
	projectIDRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// reservedServiceNames are names that could shadow LaunchKit's own routes or
// clash with cloud-provider reserved names. Blocked at plan time.
var reservedServiceNames = map[string]bool{
	"healthz":      true,
	"readyz":       true,
	"admin":        true,
	"launchkit":    true,
	"internal":     true,
	"__internal__": true,
}

// reservedEnvKeys are environment variable names that the container runtime or
// cloud provider sets internally. Allowing users to override them could leak
// credentials or break the platform.
var reservedEnvKeys = map[string]bool{
	// Cloud Run reserved (https://cloud.google.com/run/docs/reference/container-contract)
	"K_SERVICE":       true,
	"K_REVISION":      true,
	"K_CONFIGURATION": true,
	// Generic runtime
	"PORT": true,
	"HOME": true,
	"PATH": true,
	// GCP credential / metadata
	"GOOGLE_APPLICATION_CREDENTIALS": true,
	"GOOGLE_CLOUD_PROJECT":           true,
	"GCE_METADATA_HOST":              true,
	"METADATA_SERVER":                true,
	// AWS credential chain
	"AWS_ACCESS_KEY_ID":     true,
	"AWS_SECRET_ACCESS_KEY": true,
	"AWS_SESSION_TOKEN":     true,
	"AWS_DEFAULT_REGION":    true,
	"AWS_REGION":            true,
}

// ssrfBlockedDomains are domains that point to internal metadata / link-local
// infrastructure and must never be registered as custom domains.
var ssrfBlockedDomains = []string{
	"169.254.169.254",          // AWS/GCP metadata server (IP form)
	"metadata.google.internal", // GCP metadata FQDN
	"metadata.google",
	"localhost",
	"127.0.0.1",
	"0.0.0.0",
	"::1",
}

// Validate checks a Plan for structural correctness before execution.
func Validate(p *Plan) error {
	var errs []error

	if p.ID == "" {
		errs = append(errs, errors.New("plan ID is required"))
	}
	if p.ProjectID == "" {
		errs = append(errs, errors.New("project ID is required"))
	} else if !projectIDRe.MatchString(p.ProjectID) {
		// Security: non-UUID project IDs can contain ":" characters that break the
		// URN format "launchkit:{projectID}:{type}:{name}" used in resource_states.
		// Enforcing UUID format prevents URN collision / injection attacks.
		errs = append(errs, fmt.Errorf("project ID %q must be a valid UUID (xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx)", p.ProjectID))
	}

	// DoS prevention: limit services per plan
	if len(p.Services) > maxServicesPerPlan {
		errs = append(errs, fmt.Errorf("too many services: %d (max %d)", len(p.Services), maxServicesPerPlan))
	}

	if len(p.Services) == 0 {
		errs = append(errs, errors.New("at least one service is required"))
	}

	serviceNames := make(map[string]bool)
	for i, svc := range p.Services {
		prefix := fmt.Sprintf("services[%d]", i)

		if svc.Name == "" {
			errs = append(errs, fmt.Errorf("%s: name is required", prefix))
		} else if len(svc.Name) > maxServiceNameLength {
			errs = append(errs, fmt.Errorf("%s: name %q exceeds max length of %d characters", prefix, svc.Name, maxServiceNameLength))
		} else if !serviceNameRe.MatchString(svc.Name) {
			errs = append(errs, fmt.Errorf("%s: name %q must match [a-z][a-z0-9-]*", prefix, svc.Name))
		} else if reservedServiceNames[svc.Name] {
			errs = append(errs, fmt.Errorf("%s: name %q is reserved and cannot be used as a service name", prefix, svc.Name))
		}
		if serviceNames[svc.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate service name %q", prefix, svc.Name))
		}
		serviceNames[svc.Name] = true

		// DoS prevention: limit env vars per service
		if len(svc.EnvVars) > maxEnvVarsPerService {
			errs = append(errs, fmt.Errorf("%s: too many env vars: %d (max %d)", prefix, len(svc.EnvVars), maxEnvVarsPerService))
		}
		if len(svc.BuildArgs) > maxBuildArgsPerService {
			errs = append(errs, fmt.Errorf("%s: too many build args: %d (max %d)", prefix, len(svc.BuildArgs), maxBuildArgsPerService))
		}

		if svc.Type == "" {
			errs = append(errs, fmt.Errorf("%s: type is required", prefix))
		}
		if svc.Target == "" {
			errs = append(errs, fmt.Errorf("%s: target is required", prefix))
		}
		if svc.SourceDir == "" {
			errs = append(errs, fmt.Errorf("%s: source_dir is required", prefix))
		}

		// Path traversal guard: reject "..", absolute paths, and null bytes.
		// Null bytes can split paths in C-based libraries, bypassing the ".." check.
		if strings.Contains(svc.SourceDir, "\x00") ||
			strings.Contains(svc.SourceDir, "..") ||
			filepath.IsAbs(svc.SourceDir) {
			errs = append(errs, fmt.Errorf("%s: source_dir must be a relative path without '..' or null bytes", prefix))
		}

		// Validate env var key format (prevents shell injection in Cloud Build / CodeBuild buildspecs).
		// Also block reserved keys that the container runtime sets internally.
		for j, ev := range svc.EnvVars {
			if !envKeyRe.MatchString(ev.Key) {
				errs = append(errs, fmt.Errorf("%s.env_vars[%d]: key %q must match [A-Z_][A-Z0-9_]*", prefix, j, ev.Key))
			} else if reservedEnvKeys[ev.Key] {
				errs = append(errs, fmt.Errorf("%s.env_vars[%d]: key %q is reserved by the container runtime and cannot be overridden", prefix, j, ev.Key))
			}
		}
		for j, ev := range svc.BuildArgs {
			if !envKeyRe.MatchString(ev.Key) {
				errs = append(errs, fmt.Errorf("%s.build_args[%d]: key %q must match [A-Z_][A-Z0-9_]*", prefix, j, ev.Key))
			} else if reservedEnvKeys[ev.Key] {
				errs = append(errs, fmt.Errorf("%s.build_args[%d]: key %q is reserved by the container runtime and cannot be overridden", prefix, j, ev.Key))
			}
		}

		// Frontend services must target Cloudflare Pages
		if svc.Type == ServiceFrontend && svc.Target != TargetCloudflarePages {
			errs = append(errs, fmt.Errorf("%s: frontend services must target cloudflare_pages", prefix))
		}

		// Backend/worker services must target a compute platform
		if (svc.Type == ServiceBackend || svc.Type == ServiceWorker) && !IsComputeTarget(svc.Target) {
			errs = append(errs, fmt.Errorf("%s: backend/worker services must target a compute platform", prefix))
		}

		// Port must be in valid TCP range for compute services
		if IsComputeTarget(svc.Target) && (svc.Port <= 0 || svc.Port > 65535) {
			errs = append(errs, fmt.Errorf("%s: port must be between 1 and 65535 for compute services (got %d)", prefix, svc.Port))
		}
	}

	// Validate resources
	// DoS prevention: limit resources per plan
	if len(p.Resources) > maxResourcesPerPlan {
		errs = append(errs, fmt.Errorf("too many resources: %d (max %d)", len(p.Resources), maxResourcesPerPlan))
	}

	// storageGroupType normalises the three object-storage type aliases
	// ("storage", "gcs", "s3") to a canonical group key so that we can
	// enforce "at most one object-storage resource per plan".
	storageGroupType := func(rt ResourceType) ResourceType {
		switch rt {
		case ResourceStorage, ResourceGCS:
			return ResourceType("object_storage_gcs") // GCS-family alias group
		case ResourceS3:
			return ResourceType("object_storage_s3")
		default:
			return rt
		}
	}

	resourceTypes := make(map[ResourceType]bool) // keyed by canonical group type
	resourceNames := make(map[string]bool)       // keyed by logical name

	for i, res := range p.Resources {
		prefix := fmt.Sprintf("resources[%d]", i)

		if res.Type == "" {
			errs = append(errs, fmt.Errorf("%s: type is required", prefix))
		}

		// Reject duplicate resource NAMEs regardless of type.
		// Two resources with the same name produce the same scopeResourceName output,
		// causing the second provision call to collide with the first.
		if res.Name != "" && resourceNames[res.Name] {
			errs = append(errs, fmt.Errorf("%s: duplicate resource name %q — two resources with the same name produce the same cloud identifier", prefix, res.Name))
		}
		if res.Name != "" {
			resourceNames[res.Name] = true
		}

		// Reject duplicate resource TYPE groups (including "storage"/"gcs" aliases).
		// Allowing both "storage" and "gcs" would trigger two separate GCS bucket
		// creation calls for the same plan, wasting quota and causing confusion.
		canonicalType := storageGroupType(res.Type)
		if canonicalType != "" && resourceTypes[canonicalType] {
			if res.Type == ResourceStorage || res.Type == ResourceGCS {
				errs = append(errs, fmt.Errorf("%s: duplicate object-storage resource — 'storage' and 'gcs' are aliases for the same GCS bucket; include only one per plan", prefix))
			} else {
				errs = append(errs, fmt.Errorf("%s: duplicate resource type %q", prefix, res.Type))
			}
		}
		resourceTypes[canonicalType] = true

		if res.Provider == "" {
			errs = append(errs, fmt.Errorf("%s: provider is required", prefix))
		}
	}

	// Validate env var references: auto_inject sources must have matching resources
	for i, svc := range p.Services {
		for j, ev := range svc.EnvVars {
			if ev.Classification != EnvAutoInject {
				continue
			}
			if !hasMatchingResource(ev.Source, p.Resources) {
				errs = append(errs, fmt.Errorf("services[%d].env_vars[%d] (%s): auto_inject source %q has no matching resource",
					i, j, ev.Key, ev.Source))
			}
		}
	}

	return errors.Join(errs...)
}

// ─────────────────────────────────────────────────────────────────────────────
// Exported validators used by MCP tool handlers
// ─────────────────────────────────────────────────────────────────────────────

// ValidateEnvKeyForUpdate validates a user-supplied environment variable key
// submitted through the update_env MCP tool.
// It enforces:
//   - POSIX format: [A-Z_][A-Z0-9_]*
//   - Blocklist of runtime-reserved keys (PORT, HOME, K_SERVICE, etc.)
func ValidateEnvKeyForUpdate(key string) error {
	if key == "" {
		return errors.New("env key is required")
	}
	if !envKeyRe.MatchString(key) {
		return fmt.Errorf("env key %q must match [A-Z_][A-Z0-9_]* (uppercase letters, digits, underscores only)", key)
	}
	if reservedEnvKeys[key] {
		return fmt.Errorf("env key %q is reserved by the container runtime and cannot be set by users", key)
	}
	return nil
}

// ValidateCronExpression validates a standard 5-field cron expression.
// It rejects:
//   - Anything that is not exactly 5 space-separated fields
//   - Out-of-range field values
//   - Injection characters (semicolons, newlines, null bytes)
//   - "* * * * *" (every minute) — enforces a minimum interval of 5 minutes
func ValidateCronExpression(expr string) error {
	if expr == "" {
		return errors.New("cron expression is required")
	}
	// Reject control characters and injection chars
	for _, ch := range expr {
		if ch == ';' || ch == '\n' || ch == '\r' || ch == '\x00' || ch == '`' || ch == '$' {
			return fmt.Errorf("cron expression contains illegal character %q", ch)
		}
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return fmt.Errorf("cron expression must have exactly 5 fields (got %d): minute hour day month weekday", len(fields))
	}

	type fieldSpec struct {
		name string
		min  int
		max  int
	}
	specs := []fieldSpec{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day", 1, 31},
		{"month", 1, 12},
		{"weekday", 0, 7},
	}

	for i, f := range fields {
		if f == "*" {
			continue
		}
		// Handle */n step syntax
		if strings.HasPrefix(f, "*/") {
			n, err := strconv.Atoi(strings.TrimPrefix(f, "*/"))
			if err != nil || n < 1 {
				return fmt.Errorf("cron field %q (%s): invalid step value", f, specs[i].name)
			}
			if i == 0 && n < 5 {
				return fmt.Errorf("cron minimum interval is 5 minutes; '*/1' through '*/4' are not allowed")
			}
			continue
		}
		// Handle plain integer
		n, err := strconv.Atoi(f)
		if err != nil {
			return fmt.Errorf("cron field %q (%s): must be a number, *, or */n", f, specs[i].name)
		}
		if n < specs[i].min || n > specs[i].max {
			return fmt.Errorf("cron field %q (%s): value %d out of range [%d, %d]", f, specs[i].name, n, specs[i].min, specs[i].max)
		}
	}

	// Enforce minimum interval: "* * * * *" means every minute — too frequent.
	if fields[0] == "*" && fields[1] == "*" {
		return fmt.Errorf("cron expression '* * * * *' triggers every minute; minimum allowed interval is 5 minutes (use '*/5 * * * *')")
	}

	return nil
}

// ValidateDomain validates a user-supplied custom domain name before it is
// passed to the cloud provider API or stored in the DB.
// It rejects:
//   - Empty strings
//   - Domains with fewer than 2 labels (no TLD) — e.g. "localhost"
//   - IP addresses (v4 and v6)
//   - SSRF-risk internal/metadata domains
//   - Injection characters (semicolons, newlines, null bytes, $, `)
//   - Wildcard domains (unsupported)
//   - Domains exceeding RFC 1035 length limits
//   - Domains with "://" (URL accidentally passed as domain)
func ValidateDomain(domain string) error {
	if domain == "" {
		return errors.New("domain is required")
	}
	if len(domain) > maxDomainLength {
		return fmt.Errorf("domain exceeds maximum length of %d characters", maxDomainLength)
	}
	// Reject URL-style input
	if strings.Contains(domain, "://") {
		return fmt.Errorf("domain must not include a protocol (got %q)", domain)
	}
	// Reject injection characters
	for _, ch := range domain {
		if ch == ';' || ch == '\n' || ch == '\r' || ch == '\x00' || ch == '`' || ch == '$' || ch == '(' || ch == ')' || ch == '\'' || ch == '"' {
			return fmt.Errorf("domain contains illegal character %q", ch)
		}
	}
	// Reject wildcards
	if strings.HasPrefix(domain, "*.") || strings.HasPrefix(domain, "*") {
		return errors.New("wildcard domains are not supported")
	}
	// Reject port in domain (also catches IPv6 addresses like "::1")
	if strings.Contains(domain, ":") {
		return errors.New("domain must not contain a port number")
	}
	// Reject bare IP addresses (IPv4 and IPv6)
	if net.ParseIP(domain) != nil {
		return fmt.Errorf("IP addresses are not allowed as domain names; use a hostname")
	}
	// SSRF blocklist check
	lower := strings.ToLower(domain)
	for _, blocked := range ssrfBlockedDomains {
		if lower == blocked || strings.HasSuffix(lower, "."+blocked) {
			return fmt.Errorf("domain %q is not allowed (internal/metadata address)", domain)
		}
	}
	// Split and validate labels
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return fmt.Errorf("domain %q must have at least two labels (e.g. example.com)", domain)
	}
	for _, label := range labels {
		if label == "" {
			return fmt.Errorf("domain %q has an empty label", domain)
		}
		if !domainLabelRe.MatchString(label) {
			return fmt.Errorf("domain label %q contains invalid characters", label)
		}
	}
	return nil
}

// hasMatchingResource checks that an auto_inject source has a corresponding resource.
func hasMatchingResource(source string, resources []ResourcePlan) bool {
	// source is like "provision_postgres" → look for a resource of type "postgres"
	resType := strings.TrimPrefix(source, "provision_")
	for _, r := range resources {
		if string(r.Type) == resType {
			return true
		}
	}
	return false
}
