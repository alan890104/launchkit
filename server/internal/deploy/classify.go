package deploy

import (
	"fmt"
	"strings"
)

// ClassifyEnv determines how an environment variable should be resolved.
//
// Design philosophy: Claude reads the user's source code and understands the
// semantic meaning of each env var. The Plan Engine does NOT try to guess
// which key names map to which resources (users can name their env vars
// anything they want). Instead, Claude tells us via injectFrom which logical
// resource name the var should be injected from.
// We strictly validate that the resource name actually exists in the plan.
//
// Decision flow:
//   - injectFrom non-empty → auto_inject (strictly validate resource name exists)
//   - isBuildArg(key) → build_arg (deterministic prefix, no AI needed)
//   - hint == "auto_generate" && isSecretKey(key) → auto_generate
//   - hint non-empty → trust Claude's classification
//   - default → user_required (safe fallback)
func ClassifyEnv(key, hint, description, injectFrom string, availableResourceNames map[string]bool) (EnvVar, error) {
	ev := EnvVar{Key: key, Description: description}

	// Rule 1: Claude says this var should be injected from a provisioned resource.
	if injectFrom != "" {
		if !availableResourceNames[injectFrom] {
			// Strict Feedback Loop: Fail rather than silently downgrading.
			// This tells Claude that it referenced a dangling resource.
			return ev, fmt.Errorf("invalid inject_from: resource '%s' not found", injectFrom)
		}
		ev.Classification = EnvAutoInject
		ev.Source = "provision_" + injectFrom
		return ev, nil
	}

	// Rule 2: Build-time frontend vars → build arg (deterministic, no AI needed)
	if isBuildArg(key) {
		ev.Classification = EnvBuildArg
		return ev, nil
	}

	// Rule 3: Trust Claude's hint for remaining classification
	if hint != "" {
		switch string(hint) {
		case string(EnvAutoGenerate):
			// Extra safety layer: We only blindly generate a secret if Claude suggests it
			// AND it doesn't match a known provider prefix (e.g. STRIPE_API_KEY).
			if !isProviderKey(key) {
				ev.Classification = EnvAutoGenerate
				ev.Strategy = "random_hex_32"
			} else {
				ev.Classification = EnvUserRequired
			}
		case string(EnvBuildArg):
			ev.Classification = EnvBuildArg
		default:
			ev.Classification = EnvUserRequired
		}
		return ev, nil
	}

	// Rule 5: Safe default — user must supply
	ev.Classification = EnvUserRequired
	return ev, nil
}

// isBuildArg returns true for env vars that are compile-time only.
func isBuildArg(key string) bool {
	return strings.HasPrefix(key, "VITE_") ||
		strings.HasPrefix(key, "NEXT_PUBLIC_") ||
		strings.HasPrefix(key, "REACT_APP_")
}

// providerKeyPrefixes lists env var prefixes that belong to external providers.
// These are real credentials the user must supply, not random secrets to auto-generate.
// Add here when onboarding new providers.
var providerKeyPrefixes = []string{
	"NEON_",        // Neon Postgres
	"CLOUDFLARE_",  // Cloudflare R2 / Pages
	"UPSTASH_",     // Upstash Redis
	"TURSO_",       // Turso SQLite
	"PLANETSCALE_", // PlanetScale MySQL (future)
	"STRIPE_",      // Stripe payments
	"SUPABASE_",    // Supabase (future)
	"AWS_",         // AWS credentials
	"GCP_",         // GCP credentials
	"AZURE_",       // Azure credentials
	"GITHUB_",      // GitHub tokens
	"OPENAI_",      // OpenAI API
	"ANTHROPIC_",   // Anthropic API
	"SENDGRID_",    // SendGrid email
	"TWILIO_",      // Twilio SMS
}

// isProviderKey checks if an env var is clearly a third-party token
// that we should never attempt to randomly generate.
func isProviderKey(key string) bool {
	key = strings.ToUpper(key)

	exclusions := []string{
		"NEON_", "CLOUDFLARE_", "STRIPE_", "AWS_", "GCP_", "AZURE_",
		"OPENAI_", "ANTHROPIC_", "GITHUB_", "SENDGRID_", "TWILIO_",
		"SUPABASE_", "UPSTASH_", "TURSO_", "PLANETSCALE_",
	}

	// Check predefined exclusion list.
	for _, excl := range exclusions {
		if strings.HasPrefix(key, excl) {
			return true
		}
	}

	return false
}
