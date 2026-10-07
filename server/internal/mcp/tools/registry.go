package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func RegisterRegistryCredentials(srv *server.MCPServer, deps *Deps) {
	srv.AddTool(
		mcp.NewTool("get_registry_credentials",
			mcp.WithDescription(
				"Get Docker registry credentials for pushing built images. "+
					"Returns the docker login command to run locally before docker push. "+
					"Call this before building and pushing a Docker image in local build mode.",
			),
		),
		makeRegistryCredentialsHandler(deps),
	)
}

func makeRegistryCredentialsHandler(deps *Deps) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		switch deps.Config.Cloud {
		case "gcp":
			return gcpRegistryCredentials(deps)
		case "aws":
			return awsRegistryCredentials(deps)
		default:
			return mcp.NewToolResultError(fmt.Sprintf("unsupported cloud %q", deps.Config.Cloud)), nil
		}
	}
}

func gcpRegistryCredentials(deps *Deps) (*mcp.CallToolResult, error) {
	registry := deps.Config.ArtifactRegistryRepo
	if registry == "" {
		return mcp.NewToolResultError("ARTIFACT_REGISTRY_REPO not configured"), nil
	}

	// Extract the registry hostname (e.g. "us-east4-docker.pkg.dev") from the full repo path.
	// Format: {region}-docker.pkg.dev/{project}/{repo}
	registryHost := strings.SplitN(registry, "/", 2)[0]

	// Fetch an access token using gcloud (requires gcloud installed and authenticated).
	out, err := exec.Command("gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf(
			"failed to get GCP access token (is gcloud installed and authenticated?): %v", err,
		)), nil
	}
	token := strings.TrimSpace(string(out))

	loginCmd := fmt.Sprintf(
		`echo '%s' | docker login -u oauth2accesstoken --password-stdin %s`,
		token, registryHost,
	)

	data, _ := json.Marshal(map[string]any{
		"cloud":         "gcp",
		"registry":      registry,
		"registry_host": registryHost,
		"login_command": loginCmd,
		"image_prefix":  registry,
		"note":          "Run login_command first, then: docker buildx build --platform linux/amd64 --push -t {image_prefix}/{service}:{tag} .",
	})
	return mcp.NewToolResultText(string(data)), nil
}

func awsRegistryCredentials(deps *Deps) (*mcp.CallToolResult, error) {
	if deps.Config.AWSAccountID == "" || deps.Config.AWSRegion == "" {
		return mcp.NewToolResultError("AWS_ACCOUNT_ID and AWS_REGION must be configured"), nil
	}

	registry := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com/%s",
		deps.Config.AWSAccountID, deps.Config.AWSRegion, deps.Config.ECRRepositoryBase)
	ecrEndpoint := fmt.Sprintf("%s.dkr.ecr.%s.amazonaws.com",
		deps.Config.AWSAccountID, deps.Config.AWSRegion)

	loginCmd := fmt.Sprintf(
		`aws ecr get-login-password --region %s | docker login --username AWS --password-stdin %s`,
		deps.Config.AWSRegion, ecrEndpoint,
	)

	data, _ := json.Marshal(map[string]any{
		"cloud":         "aws",
		"registry":      registry,
		"registry_host": ecrEndpoint,
		"login_command": loginCmd,
		"image_prefix":  registry,
		"note":          "Run login_command first, then: docker buildx build --platform linux/amd64 --push -t {image_prefix}/{service}:{tag} .",
	})
	return mcp.NewToolResultText(string(data)), nil
}
