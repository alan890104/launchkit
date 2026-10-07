package deploy

import (
	"fmt"
	"regexp"
	"strings"
)

// Hints is the input from Claude's local project analysis.
// Claude scans the user's source code and produces these hints;
// the Plan Engine turns them into a deterministic Plan.
type Hints struct {
	ProjectName string         `json:"project_name" jsonschema:"description=The name of the launchkit project to deploy"`
	ProjectID   string         `json:"project_id,omitempty" jsonschema:"-"` // internal only
	Cloud       string         `json:"cloud,omitempty" jsonschema:"description=Target cloud provider,enum=gcp,enum=aws"`
	Services    []ServiceHint  `json:"services" jsonschema:"description=Services discovered in the project"`
	Resources   []ResourceHint `json:"resources" jsonschema:"description=Infrastructure resources required by the project"`
	EnvHints    []EnvHint      `json:"env_hints" jsonschema:"description=Environment variables discovered and their classification"`
	// SourcePath is the absolute path to the project root on the local machine running the MCP server.
	// When set, the server builds directly from local disk (no source upload required).
	// Claude should always set this when the project is available locally.
	SourcePath string `json:"source_path,omitempty" jsonschema:"description=Absolute path to project root on local disk. Set this so the server can build from local source without an upload step."`
}

// ServiceHint describes a service discovered by Claude.
type ServiceHint struct {
	Name      string `json:"name" jsonschema:"description=Name of the service e.g. api or web"`
	Type      string `json:"type" jsonschema:"description=Type of service,enum=backend,enum=frontend,enum=worker"`
	Framework string `json:"framework" jsonschema:"description=Framework used e.g. fastapi or next"`
	SourceDir string `json:"source_dir" jsonschema:"description=Relative path to the service source code"`

	BuildCmd  string `json:"build_cmd,omitempty" jsonschema:"description=Override build command if non-standard"`
	StartCmd  string `json:"start_cmd,omitempty" jsonschema:"description=Override start command if non-standard"`
	Port      int    `json:"port,omitempty" jsonschema:"description=Override application port (0 for auto-detect locally)"`
	WebSocket bool   `json:"websocket,omitempty" jsonschema:"description=True if service uses WebSocket connections"`
}

// ResourceHint describes infrastructure that Claude detected the project needs.
type ResourceHint struct {
	Name   string `json:"name" jsonschema:"description=Logical ID of the resource e.g. main_db"`
	Type   string `json:"type" jsonschema:"description=Type of resource to provision,enum=postgres,enum=redis,enum=s3,enum=gcs"`
	Reason string `json:"reason" jsonschema:"description=Why Claude thinks this is needed"`
}

// EnvHint describes an environment variable found in the project source.
type EnvHint struct {
	Key            string `json:"key" jsonschema:"description=The name of the environment variable"`
	Service        string `json:"service" jsonschema:"description=Which service uses this variable"`
	Classification string `json:"classification" jsonschema:"description=Classification of the variable,enum=auto_generate,enum=build_arg,enum=auto_inject,enum=user_required"`
	Description    string `json:"description" jsonschema:"description=Description of what this variable does"`
	InjectFrom     string `json:"inject_from,omitempty" jsonschema:"description=Optional. If injected from a resource, must exactly match the resource name (Logical ID)"`
}

const (
	maxProjectNameLength  = 63
	maxResourceNameLength = 63
)

// projectNameRe allows lowercase letters, digits, and hyphens — same as service names
// but also allows underscore for backward compatibility with existing projects.
var projectNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// resourceNameRe allows lowercase letters, digits, hyphens and underscores.
var resourceNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Validate checks the Hints structure for valid ENUMs and constraints
// to ensure the raw AI output is strictly adhering to the schema.
func (h *Hints) Validate() error {
	if h.ProjectName == "" {
		return fmt.Errorf("project_name is required")
	}

	// Project name: length and format validation
	if len(h.ProjectName) > maxProjectNameLength {
		return fmt.Errorf("project_name %q exceeds maximum length of %d characters", h.ProjectName, maxProjectNameLength)
	}
	// Reject injection characters and dangerous characters in project name
	if err := validateNameSafe("project_name", h.ProjectName); err != nil {
		return err
	}
	if !projectNameRe.MatchString(h.ProjectName) {
		return fmt.Errorf("project_name %q must start with a lowercase letter and contain only lowercase letters, digits, hyphens, or underscores", h.ProjectName)
	}

	for i, s := range h.Services {
		switch s.Type {
		case string(ServiceBackend), string(ServiceFrontend), string(ServiceWorker):
			// valid
		default:
			return fmt.Errorf("service[%d] (%s) has invalid type: %q. Must be frontend, backend, or worker", i, s.Name, s.Type)
		}
	}

	for i, r := range h.Resources {
		if r.Name == "" {
			return fmt.Errorf("resource[%d] has empty name", i)
		}
		if len(r.Name) > maxResourceNameLength {
			return fmt.Errorf("resource[%d] name %q exceeds maximum length of %d characters", i, r.Name, maxResourceNameLength)
		}
		if err := validateNameSafe(fmt.Sprintf("resource[%d].name", i), r.Name); err != nil {
			return err
		}
		if !resourceNameRe.MatchString(r.Name) {
			return fmt.Errorf("resource[%d] name %q must start with a lowercase letter and contain only lowercase letters, digits, hyphens, or underscores", i, r.Name)
		}

		switch r.Type {
		case string(ResourcePostgres), string(ResourceRedis), string(ResourceS3), string(ResourceGCS):
			// valid
		default:
			return fmt.Errorf("resource[%d] (%s) has invalid type: %q. Must be postgres, redis, s3, or gcs", i, r.Name, r.Type)
		}
	}

	for i, e := range h.EnvHints {
		switch e.Classification {
		case string(EnvAutoGenerate), string(EnvBuildArg), string(EnvAutoInject), string(EnvUserRequired):
			// valid
		default:
			return fmt.Errorf("env_hint[%d] (%s) has invalid classification: %q", i, e.Key, e.Classification)
		}
	}

	return nil
}

// validateNameSafe checks for characters that should never appear in a user-supplied
// identifier — null bytes, control characters, path separators, and common injection chars.
func validateNameSafe(field, name string) error {
	dangerous := []struct {
		char rune
		desc string
	}{
		{'\x00', "null byte"},
		{'\n', "newline"},
		{'\r', "carriage return"},
		{'/', "forward slash"},
		{'\\', "backslash"},
		{';', "semicolon"},
		{'\'', "single quote"},
		{'"', "double quote"},
		{'$', "dollar sign"},
		{'`', "backtick"},
		{'(', "parenthesis"},
		{')', "parenthesis"},
		{'\u202E', "Unicode RTL override"},
	}
	for _, d := range dangerous {
		if strings.ContainsRune(name, d.char) {
			return fmt.Errorf("%s %q contains illegal character: %s", field, name, d.desc)
		}
	}
	return nil
}
