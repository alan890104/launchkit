package provider

import (
	"context"
	"encoding/base64"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ecr"
	"github.com/docker/cli/cli/config/types"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth/authprovider"
	"golang.org/x/oauth2/google"
)

// gcpRegistryAuth returns a BuildKit session.Attachable that provides
// GCP Application Default Credentials for Artifact Registry pushes.
// Handles hosts matching "*-docker.pkg.dev" and "gcr.io".
func gcpRegistryAuth(ctx context.Context) session.Attachable {
	return authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: func(ctx context.Context, host string, _ []string, _ authprovider.ExpireCachedAuthCheck) (types.AuthConfig, error) {
			if !strings.HasSuffix(host, "-docker.pkg.dev") && host != "gcr.io" {
				return types.AuthConfig{}, nil
			}
			ts, err := google.DefaultTokenSource(ctx,
				"https://www.googleapis.com/auth/cloud-platform",
			)
			if err != nil {
				return types.AuthConfig{}, err
			}
			tok, err := ts.Token()
			if err != nil {
				return types.AuthConfig{}, err
			}
			return types.AuthConfig{
				Username: "oauth2accesstoken",
				Password: tok.AccessToken,
			}, nil
		},
	})
}

// ecrRegistryAuth returns a BuildKit session.Attachable that provides
// AWS ECR credentials via GetAuthorizationToken for image pushes.
// Handles hosts matching "*.dkr.ecr.*.amazonaws.com".
func ecrRegistryAuth(region string) session.Attachable {
	return authprovider.NewDockerAuthProvider(authprovider.DockerAuthProviderConfig{
		AuthConfigProvider: func(ctx context.Context, host string, _ []string, _ authprovider.ExpireCachedAuthCheck) (types.AuthConfig, error) {
			if !strings.Contains(host, ".dkr.ecr.") {
				return types.AuthConfig{}, nil
			}
			awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
			if err != nil {
				return types.AuthConfig{}, err
			}
			out, err := ecr.NewFromConfig(awsCfg).GetAuthorizationToken(ctx, &ecr.GetAuthorizationTokenInput{})
			if err != nil {
				return types.AuthConfig{}, err
			}
			if len(out.AuthorizationData) == 0 {
				return types.AuthConfig{}, nil
			}
			decoded, err := base64.StdEncoding.DecodeString(*out.AuthorizationData[0].AuthorizationToken)
			if err != nil {
				return types.AuthConfig{}, err
			}
			user, pass, _ := strings.Cut(string(decoded), ":")
			return types.AuthConfig{
				Username: user,
				Password: pass,
			}, nil
		},
	})
}
