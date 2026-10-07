package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	artifactregistrypb "cloud.google.com/go/artifactregistry/apiv1/artifactregistrypb"
	"google.golang.org/api/iterator"
)

// ARFetcher implements ConsumptionFetcher for Google Artifact Registry.
//
// All user images share a single AR repository (e.g. launchkit-images).
// Cloud Monitoring only provides repo-level storage totals, which is useless
// for per-user billing. Instead, this fetcher uses the Artifact Registry API's
// ListDockerImages to get exact per-image sizes, then groups by service name
// (the image path within the repo) to compute per-user storage.
//
// Image URI format: {region}-docker.pkg.dev/{project}/{repo}/{serviceName}:{tag}
// where serviceName = "lk-{projectName}-{svcName}" — unique per user service.
type ARFetcher struct {
	projectID string
}

var _ ConsumptionFetcher = (*ARFetcher)(nil)

func NewARFetcher(projectID string) *ARFetcher {
	return &ARFetcher{projectID: projectID}
}

func (f *ARFetcher) ProviderName() string { return "artifact_registry" }

func (f *ARFetcher) FetchConsumption(
	ctx context.Context,
	resources []ActiveResource,
	start, end time.Time,
) ([]ConsumptionRecord, error) {
	if len(resources) == 0 {
		return nil, nil
	}

	// Parse image URIs to identify repository and service name for each resource.
	type resourceInfo struct {
		res         ActiveResource
		repoParent  string // "projects/{project}/locations/{region}/repositories/{repo}"
		serviceName string // image path within repo, e.g. "lk-myapp-api"
	}

	var infos []resourceInfo
	repoParents := make(map[string]bool)

	for _, res := range resources {
		imageURI := OutputStr(res.Outputs, "image_uri")
		if imageURI == "" {
			continue
		}

		repoParent, svcName, err := parseARImageURI(f.projectID, imageURI)
		if err != nil {
			slog.Warn("artifact_registry: could not parse image URI, skipping",
				"urn", res.URN, "image_uri", imageURI, "error", err)
			continue
		}

		infos = append(infos, resourceInfo{
			res:         res,
			repoParent:  repoParent,
			serviceName: svcName,
		})
		repoParents[repoParent] = true
	}

	if len(infos) == 0 {
		return nil, nil
	}

	// Query AR API for each repository to get per-image sizes.
	// Group by service name (image path) for per-user attribution.
	// Key: repoParent + "/" + serviceName → total bytes
	svcSizes := make(map[string]int64)

	client, err := artifactregistry.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("create artifact registry client: %w", err)
	}
	defer client.Close()

	for repoParent := range repoParents {
		if err := f.fetchRepoImageSizes(ctx, client, repoParent, svcSizes); err != nil {
			slog.Warn("artifact_registry: list images failed",
				"repo", repoParent, "error", err)
			// Continue with other repos
		}
	}

	// Build consumption records for each resource.
	var records []ConsumptionRecord

	for _, info := range infos {
		key := info.repoParent + "/" + info.serviceName
		totalBytes := svcSizes[key]
		if totalBytes <= 0 {
			continue
		}

		region := OutputStr(info.res.Outputs, "region")
		if region == "" {
			region = arRegionFromRepoParent(info.repoParent)
		}

		records = append(records, ConsumptionRecord{
			ResourceURN:  info.res.URN,
			ProjectID:    info.res.ProjectID,
			ResourceType: "storage",
			Provider:     "artifact_registry",
			ProviderID:   info.repoParent,
			Region:       region,
			PeriodStart:  start,
			PeriodEnd:    end,
			Metrics: map[string]float64{
				"storage_bytes": float64(totalBytes),
			},
		})
	}

	return records, nil
}

// fetchRepoImageSizes lists all Docker images in a repository and accumulates
// sizes by service name (image package path).
//
// AR image names look like:
//
//	projects/my-proj/locations/us-east4/repositories/my-repo/dockerImages/lk-userA-api@sha256:abc
//
// The "package" (service name) is extracted from the path between dockerImages/ and @.
func (f *ARFetcher) fetchRepoImageSizes(
	ctx context.Context,
	client *artifactregistry.Client,
	repoParent string,
	svcSizes map[string]int64,
) error {
	it := client.ListDockerImages(ctx, &artifactregistrypb.ListDockerImagesRequest{
		Parent: repoParent,
	})

	for {
		img, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return fmt.Errorf("list docker images: %w", err)
		}

		// img.Name format:
		// "projects/p/locations/l/repositories/r/dockerImages/SERVICE_NAME@sha256:..."
		// or with tag: "projects/p/locations/l/repositories/r/dockerImages/SERVICE_NAME:TAG"
		svcName := extractServiceNameFromImageName(img.GetName())
		if svcName == "" {
			continue
		}

		key := repoParent + "/" + svcName
		svcSizes[key] += img.GetImageSizeBytes()
	}

	return nil
}

// extractServiceNameFromImageName extracts the service name from an AR image resource name.
// Input:  "projects/p/locations/l/repositories/r/dockerImages/lk-myapp-api@sha256:abc"
// Output: "lk-myapp-api"
func extractServiceNameFromImageName(name string) string {
	// Find the part after "dockerImages/"
	idx := strings.Index(name, "dockerImages/")
	if idx < 0 {
		return ""
	}
	rest := name[idx+len("dockerImages/"):]

	// Strip digest (@sha256:...) or tag (:...)
	if at := strings.IndexByte(rest, '@'); at >= 0 {
		rest = rest[:at]
	}
	if colon := strings.IndexByte(rest, ':'); colon >= 0 {
		rest = rest[:colon]
	}

	return rest
}

// parseARImageURI extracts the repo resource name and service name from an image URI.
//
// Input:  "us-east4-docker.pkg.dev/my-proj/my-repo/lk-myapp-api:abc123"
// Output: ("projects/my-proj/locations/us-east4/repositories/my-repo", "lk-myapp-api", nil)
func parseARImageURI(gcpProject, imageURI string) (repoParent, serviceName string, err error) {
	// Strip tag or digest
	base := strings.SplitN(imageURI, "@", 2)[0]
	base = strings.SplitN(base, ":", 2)[0]

	// Expected format: {region}-docker.pkg.dev/{project}/{repo}/{serviceName}
	parts := strings.SplitN(base, "/", 4)
	if len(parts) < 4 {
		return "", "", fmt.Errorf("unexpected image URI format: %q", imageURI)
	}

	host := parts[0] // "us-east4-docker.pkg.dev"
	repo := parts[2] // "my-repo"
	svc := parts[3]  // "lk-myapp-api"

	region := strings.TrimSuffix(host, "-docker.pkg.dev")
	if region == host {
		return "", "", fmt.Errorf("unexpected AR host format: %q", host)
	}

	repoParent = fmt.Sprintf("projects/%s/locations/%s/repositories/%s",
		gcpProject, region, repo)

	return repoParent, svc, nil
}

// arRegionFromRepoParent extracts the region from a repo resource name.
func arRegionFromRepoParent(repoParent string) string {
	// "projects/p/locations/us-east4/repositories/r" → "us-east4"
	parts := strings.Split(repoParent, "/")
	for i, p := range parts {
		if p == "locations" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
