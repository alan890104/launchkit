package deploy

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestClassifyEnv(t *testing.T) {
	withResources := func(types ...string) map[string]bool {
		m := make(map[string]bool)
		for _, t := range types {
			m[t] = true
		}
		return m
	}

	noResources := map[string]bool{}

	tests := []struct {
		name          string
		key           string
		hint          string
		description   string
		injectFrom    string
		resourceNames map[string]bool
		want          EnvVar
		wantErr       string
	}{
		// ── Rule 1: injectFrom non-empty + resource exists → auto_inject ──
		{
			name:          "injectFrom=main_db with resource → auto_inject",
			key:           "DATABASE_URL",
			injectFrom:    "main_db",
			resourceNames: withResources("main_db"),
			want: EnvVar{
				Key:            "DATABASE_URL",
				Classification: EnvAutoInject,
				Source:         "provision_main_db",
			},
		},
		{
			name:          "arbitrary key name (POSTGRES_DSN) works via injectFrom",
			key:           "POSTGRES_DSN",
			injectFrom:    "main_db",
			resourceNames: withResources("main_db"),
			want: EnvVar{
				Key:            "POSTGRES_DSN",
				Classification: EnvAutoInject,
				Source:         "provision_main_db",
			},
		},
		{
			name:          "completely custom key name (MY_DB_CONN) works via injectFrom",
			key:           "MY_DB_CONN",
			injectFrom:    "main_db",
			resourceNames: withResources("main_db"),
			want: EnvVar{
				Key:            "MY_DB_CONN",
				Classification: EnvAutoInject,
				Source:         "provision_main_db",
			},
		},
		{
			name:          "injectFrom=cache with resource → auto_inject",
			key:           "REDIS_URL",
			injectFrom:    "cache",
			resourceNames: withResources("cache"),
			want: EnvVar{
				Key:            "REDIS_URL",
				Classification: EnvAutoInject,
				Source:         "provision_cache",
			},
		},
		{
			name:          "arbitrary redis key name (CACHE_URL) works via injectFrom",
			key:           "CACHE_URL",
			injectFrom:    "cache",
			resourceNames: withResources("cache"),
			want: EnvVar{
				Key:            "CACHE_URL",
				Classification: EnvAutoInject,
				Source:         "provision_cache",
			},
		},
		{
			name:          "future resource type (mysql) works via injectFrom",
			key:           "MYSQL_CONN",
			injectFrom:    "sql_db",
			resourceNames: withResources("sql_db"),
			want: EnvVar{
				Key:            "MYSQL_CONN",
				Classification: EnvAutoInject,
				Source:         "provision_sql_db",
			},
		},
		{
			name:          "future resource type (storage) works via injectFrom",
			key:           "S3_BUCKET_URL",
			injectFrom:    "bucket1",
			resourceNames: withResources("bucket1"),
			want: EnvVar{
				Key:            "S3_BUCKET_URL",
				Classification: EnvAutoInject,
				Source:         "provision_bucket1",
			},
		},

		// ── Strict Feedback Loop: resource doesn't exist → Error ──
		{
			name:          "injectFrom=main_db but no resource → Error",
			key:           "DATABASE_URL",
			injectFrom:    "main_db",
			resourceNames: noResources,
			wantErr:       "invalid inject_from: resource 'main_db' not found",
		},
		{
			name:          "injectFrom=cache but no resource → Error",
			key:           "REDIS_URL",
			injectFrom:    "cache",
			resourceNames: noResources,
			wantErr:       "invalid inject_from: resource 'cache' not found",
		},

		// ── injectFrom wins over everything (even build_arg prefix) ──
		{
			name:          "injectFrom wins over VITE_ build_arg prefix",
			key:           "VITE_DB_URL",
			injectFrom:    "main_db",
			resourceNames: withResources("main_db"),
			want: EnvVar{
				Key:            "VITE_DB_URL",
				Classification: EnvAutoInject,
				Source:         "provision_main_db",
			},
		},

		// ── Rule 2: Build-time frontend vars ──
		{
			name:          "VITE_ prefix → build_arg",
			key:           "VITE_API_URL",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "VITE_API_URL",
				Classification: EnvBuildArg,
			},
		},
		{
			name:          "NEXT_PUBLIC_ prefix → build_arg",
			key:           "NEXT_PUBLIC_API",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "NEXT_PUBLIC_API",
				Classification: EnvBuildArg,
			},
		},
		{
			name:          "REACT_APP_ prefix → build_arg",
			key:           "REACT_APP_KEY",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "REACT_APP_KEY",
				Classification: EnvBuildArg,
			},
		},

		// ── Rule 3: Trust Claude for auto_generate (removed blind guessing) ──
		{
			name:          "SECRET_KEY suffix without hint → user_required",
			key:           "SECRET_KEY",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "SECRET_KEY",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "JWT_TOKEN suffix without hint → user_required",
			key:           "JWT_TOKEN",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "JWT_TOKEN",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "API_PASSWORD suffix with hint auto_generate → auto_generate",
			key:           "API_PASSWORD",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "API_PASSWORD",
				Classification: EnvAutoGenerate,
				Strategy:       "random_hex_32",
			},
		},
		{
			name:          "APP_SECRET suffix with hint auto_generate → auto_generate",
			key:           "APP_SECRET",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "APP_SECRET",
				Classification: EnvAutoGenerate,
				Strategy:       "random_hex_32",
			},
		},

		// ── Provider prefix exclusions (safety even with auto_generate hint) ──
		{
			name:          "NEON_API_KEY with auto_generate hint blocked → user_required",
			key:           "NEON_API_KEY",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "NEON_API_KEY",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "CLOUDFLARE_API_TOKEN with auto_generate hint blocked → user_required",
			key:           "CLOUDFLARE_API_TOKEN",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "CLOUDFLARE_API_TOKEN",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "STRIPE_SECRET_KEY with auto_generate hint blocked → user_required",
			key:           "STRIPE_SECRET_KEY",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "STRIPE_SECRET_KEY",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "AWS_SECRET_KEY with auto_generate hint blocked → user_required",
			key:           "AWS_SECRET_KEY",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "AWS_SECRET_KEY",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "OPENAI_API_KEY with auto_generate hint blocked → user_required",
			key:           "OPENAI_API_KEY",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "OPENAI_API_KEY",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "GITHUB_TOKEN with auto_generate hint blocked → user_required",
			key:           "GITHUB_TOKEN",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "GITHUB_TOKEN",
				Classification: EnvUserRequired,
			},
		},

		// ── Near-miss: NEON_ prefix + _SECRET suffix, but _SECRET is checked ──
		{
			name:          "NEON_SECRET excluded by prefix → user_required",
			key:           "NEON_SECRET",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "NEON_SECRET",
				Classification: EnvUserRequired,
			},
		},

		// ── Rule 4: Claude hint fallback ──
		{
			name:          "hint build_arg → build_arg",
			key:           "CUSTOM_VAR",
			hint:          "build_arg",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "CUSTOM_VAR",
				Classification: EnvBuildArg,
			},
		},
		{
			name:          "garbage hint → user_required",
			key:           "CUSTOM_VAR",
			hint:          "garbage",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "CUSTOM_VAR",
				Classification: EnvUserRequired,
			},
		},
		{
			name:          "hint auto_inject without injectFrom → user_required",
			key:           "DATABASE_URL",
			hint:          "auto_inject",
			resourceNames: withResources("postgres"),
			want: EnvVar{
				Key:            "DATABASE_URL",
				Classification: EnvUserRequired,
			},
		},

		// ── Rule 5: Default fallback ──
		{
			name:          "no hint, no match → user_required",
			key:           "CUSTOM_VAR",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "CUSTOM_VAR",
				Classification: EnvUserRequired,
			},
		},

		// ── Priority: build_arg > secret ──
		{
			name:          "VITE_ prefix wins over hint auto_generate",
			key:           "VITE_SECRET_KEY",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "VITE_SECRET_KEY",
				Classification: EnvBuildArg,
			},
		},
		{
			name:          "NEXT_PUBLIC_ prefix wins over hint auto_generate",
			key:           "NEXT_PUBLIC_TOKEN",
			hint:          "auto_generate",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "NEXT_PUBLIC_TOKEN",
				Classification: EnvBuildArg,
			},
		},

		// ── Description passthrough ──
		{
			name:          "description passed through",
			key:           "MY_VAR",
			description:   "some desc",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "MY_VAR",
				Classification: EnvUserRequired,
				Description:    "some desc",
			},
		},

		// ── Edge: empty key ──
		{
			name:          "empty key → user_required",
			key:           "",
			resourceNames: noResources,
			want: EnvVar{
				Key:            "",
				Classification: EnvUserRequired,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ClassifyEnv(tt.key, tt.hint, tt.description, tt.injectFrom, tt.resourceNames)
			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

func TestIsProviderKey(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		// Provider keys that should return true
		{"NEON_API_KEY", true},
		{"NEON_SECRET", true},
		{"CLOUDFLARE_API_TOKEN", true},
		{"STRIPE_SECRET_KEY", true},
		{"STRIPE_API_KEY", true},
		{"AWS_SECRET_KEY", true},
		{"GCP_API_KEY", true},
		{"AZURE_API_KEY", true},
		{"OPENAI_API_KEY", true},
		{"ANTHROPIC_API_KEY", true},
		{"GITHUB_TOKEN", true},
		{"SENDGRID_API_KEY", true},
		{"TWILIO_AUTH_TOKEN", true},
		{"SUPABASE_API_KEY", true},
		{"UPSTASH_TOKEN", true},
		{"TURSO_AUTH_TOKEN", true},
		{"PLANETSCALE_PASSWORD", true},

		// Non-provider keys that can be safely auto-generated if asked
		{"SECRET_KEY", false},
		{"JWT_SECRET", false},
		{"SESSION_SECRET", false},
		{"APP_SECRET", false},
		{"JWT_TOKEN", false},
		{"API_PASSWORD", false},
		{"ADMIN_PASSWORD", false},
		{"ENCRYPTION_KEY", false},
		{"DATABASE_URL", false},
		{"PORT", false},
		{"NODE_ENV", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, isProviderKey(tt.key))
		})
	}
}

func TestIsBuildArg(t *testing.T) {
	tests := []struct {
		key  string
		want bool
	}{
		{"VITE_API_URL", true},
		{"VITE_SECRET_KEY", true},
		{"NEXT_PUBLIC_API", true},
		{"REACT_APP_KEY", true},
		{"DATABASE_URL", false},
		{"SECRET_KEY", false},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			assert.Equal(t, tt.want, isBuildArg(tt.key))
		})
	}
}
