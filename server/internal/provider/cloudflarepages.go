package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lukechampine.com/blake3"
)

// Compile-time interface guard.
var _ Static = (*CloudflarePages)(nil)

const deployMaxBytes = 100 * 1024 * 1024 // 100 MB

const cfAPIBase = "https://api.cloudflare.com/client/v4"

// CloudflarePages implements Static using the Cloudflare Pages API.
type CloudflarePages struct {
	accountID string
	apiToken  string
	client    *http.Client
}

func NewCloudflarePages(accountID, apiToken string) *CloudflarePages {
	return &CloudflarePages{
		accountID: accountID,
		apiToken:  apiToken,
		client:    &http.Client{Timeout: 120 * time.Second},
	}
}

func (cf *CloudflarePages) CreateProject(ctx context.Context, projectName string) error {
	body := map[string]any{
		"name":              projectName,
		"production_branch": "main",
	}

	resp, err := cf.doJSON(ctx, "POST", cf.projectsURL(), body)
	if err != nil {
		return fmt.Errorf("create Pages project: %w", err)
	}
	defer resp.Body.Close()

	// 409 = already exists, which is fine (idempotent)
	if resp.StatusCode == http.StatusConflict {
		return nil
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create Pages project: status %d: %s", resp.StatusCode, respBody)
	}

	return nil
}

// fileEntry holds info for a single file to upload.
type fileEntry struct {
	relPath string // e.g. "/index.html"
	hash    string // BLAKE3 hash matching Wrangler's format (32 hex chars)
	content []byte
}

// pagesHash computes the Wrangler-compatible BLAKE3 hash for a file.
// Wrangler hashes: blake3(base64(content) + extension)[:32 hex chars].
func pagesHash(content []byte, relPath string) string {
	ext := filepath.Ext(relPath)
	if len(ext) > 0 {
		ext = ext[1:] // strip leading dot: ".html" → "html"
	}
	input := base64.StdEncoding.EncodeToString(content) + ext
	h := blake3.Sum256([]byte(input))
	return hex.EncodeToString(h[:])[:32]
}

func (cf *CloudflarePages) Deploy(ctx context.Context, opts DeployStaticOpts) (string, error) {
	absDistDir, err := filepath.Abs(opts.DistDir)
	if err != nil {
		return "", fmt.Errorf("resolve dist dir: %w", err)
	}
	allowedBase := filepath.Clean(os.TempDir())
	if !strings.HasPrefix(absDistDir+string(os.PathSeparator), allowedBase+string(os.PathSeparator)) {
		return "", fmt.Errorf("dist dir %q is outside the allowed base directory %q", absDistDir, allowedBase)
	}

	// Collect all files with BLAKE3 hashes (Wrangler-compatible format).
	var files []fileEntry
	var totalSize int64
	err = filepath.Walk(absDistDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Skip macOS resource fork files (e.g. ._icons.svg)
		if strings.HasPrefix(info.Name(), "._") {
			return nil
		}
		totalSize += info.Size()
		if totalSize > deployMaxBytes {
			return fmt.Errorf("dist directory exceeds %d bytes", deployMaxBytes)
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		relPath, err := filepath.Rel(absDistDir, path)
		if err != nil {
			return err
		}
		relPath = "/" + strings.ReplaceAll(relPath, string(os.PathSeparator), "/")

		files = append(files, fileEntry{
			relPath: relPath,
			hash:    pagesHash(content, relPath),
			content: content,
		})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk dist: %w", err)
	}

	slog.Info("cloudflare pages upload", "files", len(files), "total_bytes", totalSize)

	// Step 1: Get upload JWT from Cloudflare.
	jwt, err := cf.getUploadToken(ctx, opts.ProjectName)
	if err != nil {
		return "", fmt.Errorf("get upload token: %w", err)
	}

	// Step 2: Upload files to Cloudflare's asset storage.
	// Check which files are missing first to avoid re-uploading.
	hashes := make([]string, len(files))
	for i, f := range files {
		hashes[i] = f.hash
	}
	missing, err := cf.checkMissing(ctx, jwt, hashes)
	if err != nil {
		return "", fmt.Errorf("check missing: %w", err)
	}
	if len(missing) > 0 {
		missingSet := make(map[string]bool, len(missing))
		for _, h := range missing {
			missingSet[h] = true
		}
		var toUpload []fileEntry
		for _, f := range files {
			if missingSet[f.hash] {
				toUpload = append(toUpload, f)
			}
		}
		slog.Info("cloudflare pages uploading assets", "total", len(files), "missing", len(toUpload))
		if err := cf.uploadAssets(ctx, jwt, toUpload); err != nil {
			return "", fmt.Errorf("upload assets: %w", err)
		}
	}

	// Step 3: Upsert hashes to finalize asset references.
	if err := cf.upsertHashes(ctx, jwt, hashes); err != nil {
		return "", fmt.Errorf("upsert hashes: %w", err)
	}

	// Step 4: Create deployment with manifest only (no files in form).
	manifest := make(map[string]string, len(files))
	for _, f := range files {
		manifest[f.relPath] = f.hash
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	manifestJSON, _ := json.Marshal(manifest)
	if err := writer.WriteField("manifest", string(manifestJSON)); err != nil {
		return "", fmt.Errorf("write manifest: %w", err)
	}
	writer.Close()

	deployURL := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/deployments",
		cfAPIBase, cf.accountID, opts.ProjectName)
	req, err := http.NewRequestWithContext(ctx, "POST", deployURL, body)
	if err != nil {
		return "", fmt.Errorf("create deploy request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cf.apiToken)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := cf.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("deploy to Pages: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("deploy to Pages: status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Result struct {
			URL string `json:"url"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode deploy response: %w", err)
	}

	slog.Info("cloudflare pages deployed", "url", result.Result.URL)
	return result.Result.URL, nil
}

// getUploadToken gets a JWT for uploading assets to Cloudflare Pages.
func (cf *CloudflarePages) getUploadToken(ctx context.Context, projectName string) (string, error) {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s/upload-token",
		cfAPIBase, cf.accountID, projectName)

	resp, err := cf.doGet(ctx, url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("get upload token: status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Result struct {
			JWT string `json:"jwt"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.Result.JWT, nil
}

// upsertHashes finalizes asset references after upload.
// Note: asset endpoints use /pages/assets/... without the account ID prefix.
func (cf *CloudflarePages) upsertHashes(ctx context.Context, jwt string, hashes []string) error {
	payload, _ := json.Marshal(map[string][]string{"hashes": hashes})

	req, err := http.NewRequestWithContext(ctx, "POST", cfAPIBase+"/pages/assets/upsert-hashes", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cf.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upsert-hashes: status %d: %s", resp.StatusCode, body)
	}
	return nil
}

// checkMissing asks Cloudflare which file hashes haven't been uploaded yet.
// Note: asset endpoints use /pages/assets/... without the account ID prefix.
func (cf *CloudflarePages) checkMissing(ctx context.Context, jwt string, hashes []string) ([]string, error) {
	payload, _ := json.Marshal(map[string][]string{"hashes": hashes})

	req, err := http.NewRequestWithContext(ctx, "POST", cfAPIBase+"/pages/assets/check-missing", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Content-Type", "application/json")

	resp, err := cf.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("check-missing: status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Result []string `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Result, nil
}

// uploadAssets uploads file contents to Cloudflare's asset storage.
// Note: asset endpoints use /pages/assets/... without the account ID prefix.
func (cf *CloudflarePages) uploadAssets(ctx context.Context, jwt string, files []fileEntry) error {
	// Upload in batches of 50 files (Cloudflare limit)
	const batchSize = 50
	for i := 0; i < len(files); i += batchSize {
		end := i + batchSize
		if end > len(files) {
			end = len(files)
		}
		batch := files[i:end]

		var payload []map[string]any
		for _, f := range batch {
			payload = append(payload, map[string]any{
				"key":      f.hash,
				"value":    base64.StdEncoding.EncodeToString(f.content),
				"metadata": map[string]string{"contentType": detectContentType(f.relPath)},
				"base64":   true,
			})
		}

		data, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, "POST", cfAPIBase+"/pages/assets/upload", bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+jwt)
		req.Header.Set("Content-Type", "application/json")

		resp, err := cf.client.Do(req)
		if err != nil {
			return err
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("upload assets batch: status %d: %s", resp.StatusCode, body)
		}
	}
	return nil
}

func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".html":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "application/javascript"
	case ".json":
		return "application/json"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".ico":
		return "image/x-icon"
	case ".woff", ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain"
	default:
		return "application/octet-stream"
	}
}

func (cf *CloudflarePages) DeleteProject(ctx context.Context, projectName string) error {
	url := fmt.Sprintf("%s/accounts/%s/pages/projects/%s", cfAPIBase, cf.accountID, projectName)

	req, err := http.NewRequestWithContext(ctx, "DELETE", url, nil)
	if err != nil {
		return fmt.Errorf("create delete request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cf.apiToken)

	resp, err := cf.client.Do(req)
	if err != nil {
		return fmt.Errorf("delete Pages project: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("delete Pages project: status %d: %s", resp.StatusCode, respBody)
	}

	return nil
}

func (cf *CloudflarePages) doJSON(ctx context.Context, method, url string, body any) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cf.apiToken)
	req.Header.Set("Content-Type", "application/json")

	return cf.client.Do(req)
}

func (cf *CloudflarePages) doGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cf.apiToken)
	return cf.client.Do(req)
}

func (cf *CloudflarePages) projectsURL() string {
	return fmt.Sprintf("%s/accounts/%s/pages/projects", cfAPIBase, cf.accountID)
}
