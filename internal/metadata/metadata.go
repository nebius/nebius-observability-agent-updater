package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Path                       string `yaml:"path"`
	ParentIdFilename           string `yaml:"parent_id_filename"`
	InstanceIdFilename         string `yaml:"instance_id_filename"`
	IamTokenFilename           string `yaml:"iam_token_filename"`
	Mk8sClusterIdFilename      string `yaml:"mk8s_cluster_id_filename"`
	UseMetadataService         bool   `yaml:"use_metadata_service"`
	MetadataServiceURL         string `yaml:"metadata_service_url"`
	MetadataServiceFallbackURL string `yaml:"metadata_service_fallback_url"`
	MetadataTokenType          string `yaml:"metadata_token_type"`
}

type instanceData struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
}

const instanceDataCacheTTL = 5 * time.Minute

// tokenRefreshMargin is how long before expiry we refresh the token
const tokenRefreshMargin = 1 * time.Hour

type cachedToken struct {
	token     string
	expiresAt time.Time
}

type Reader struct {
	cfg    Config
	logger *slog.Logger
	client *http.Client

	mu              sync.Mutex
	cachedInstance  *instanceData
	cachedFetchedAt time.Time

	tokenMu   sync.Mutex
	cachedIAM *cachedToken
}

func NewReader(cfg Config, logger *slog.Logger) *Reader {
	return &Reader{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (r *Reader) GetParentId() (string, error) {
	if r.cfg.UseMetadataService {
		data, err := r.getInstanceData()
		if err == nil {
			return data.ParentID, nil
		}
		r.logger.Warn("Failed to get parent_id from IMDS, falling back to file", "error", err)
	}
	return r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.ParentIdFilename)
}

func (r *Reader) GetInstanceId() (instanceId string, isFallback bool, err error) {
	instanceId, err = r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.InstanceIdFilename)
	if err == nil {
		return instanceId, false, nil
	}
	r.logger.Warn("Failed to get instance_id from file, falling back to IMDS", "error", err)

	if r.cfg.UseMetadataService {
		data, imdsErr := r.getInstanceData()
		if imdsErr == nil {
			return data.ID, true, nil
		}
		return "", true, fmt.Errorf("failed to get instance_id from file: %w and from IMDS: %w", err, imdsErr)
	}
	return "", false, err
}

func (r *Reader) GetIamToken() (string, error) {
	if r.cfg.UseMetadataService {
		token, err := r.getCachedIAMToken()
		if err == nil {
			return token, nil
		}
		r.logger.Warn("Failed to get IAM token from IMDS, falling back to file", "error", err)
	}
	return r.readAndTrimFile(r.cfg.Path + "/" + r.cfg.IamTokenFilename)
}

func (r *Reader) getCachedIAMToken() (string, error) {
	r.tokenMu.Lock()
	defer r.tokenMu.Unlock()

	if r.cachedIAM != nil && time.Until(r.cachedIAM.expiresAt) > tokenRefreshMargin {
		return r.cachedIAM.token, nil
	}

	tokenPath := fmt.Sprintf("/v1/iam/%s/token/access_token", r.cfg.MetadataTokenType)
	body, err := r.fetchFromMetadataService(tokenPath)
	if err != nil {
		if r.cachedIAM != nil && time.Until(r.cachedIAM.expiresAt) > 0 {
			r.logger.Warn("Failed to refresh IAM token, using cached token until expiry", "error", err, "expires_at", r.cachedIAM.expiresAt)
			return r.cachedIAM.token, nil
		}
		return "", fmt.Errorf("failed to fetch IAM token from IMDS: %w", err)
	}
	token := strings.TrimSpace(string(body))

	expiresAt, err := r.fetchTokenExpiresAt()
	if err != nil {
		r.logger.Warn("Failed to get token expiry from IMDS, using default TTL", "error", err)
		expiresAt = time.Now().Add(instanceDataCacheTTL)
	}

	if time.Until(expiresAt) <= 0 {
		return "", fmt.Errorf("token from IMDS is already expired (expires_at: %s)", expiresAt.Format(time.RFC3339Nano))
	}

	r.cachedIAM = &cachedToken{
		token:     token,
		expiresAt: expiresAt,
	}
	return token, nil
}

func (r *Reader) fetchTokenExpiresAt() (time.Time, error) {
	expiresAtPath := fmt.Sprintf("/v1/iam/%s/token/expires_at", r.cfg.MetadataTokenType)
	body, err := r.fetchFromMetadataService(expiresAtPath)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to fetch token expires_at: %w", err)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(body)))
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse expires_at timestamp: %w", err)
	}
	return expiresAt, nil
}

func (r *Reader) getInstanceData() (*instanceData, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cachedInstance != nil && time.Since(r.cachedFetchedAt) < instanceDataCacheTTL {
		return r.cachedInstance, nil
	}

	body, err := r.fetchFromMetadataService("/v1/instance-data")
	if err != nil {
		if r.cachedInstance != nil {
			r.logger.Warn("Failed to refresh instance-data from IMDS, using stale cache", "error", err)
			return r.cachedInstance, nil
		}
		return nil, fmt.Errorf("failed to fetch instance-data from IMDS: %w", err)
	}

	var data instanceData
	if err := json.Unmarshal(body, &data); err != nil {
		if r.cachedInstance != nil {
			r.logger.Warn("Failed to parse instance-data JSON, using stale cache", "error", err)
			return r.cachedInstance, nil
		}
		return nil, fmt.Errorf("failed to parse instance-data JSON: %w", err)
	}

	r.cachedInstance = &data
	r.cachedFetchedAt = time.Now()
	return r.cachedInstance, nil
}

func (r *Reader) fetchFromMetadataService(path string) ([]byte, error) {
	urls := []string{r.cfg.MetadataServiceURL, r.cfg.MetadataServiceFallbackURL}
	var lastErr error
	for _, baseURL := range urls {
		body, err := r.doMetadataRequest(baseURL + path)
		if err == nil {
			return body, nil
		}
		lastErr = err
		r.logger.Debug("IMDS request failed", "url", baseURL+path, "error", err)
	}
	return nil, fmt.Errorf("all IMDS URLs failed: %w", lastErr)
}

func (r *Reader) doMetadataRequest(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Metadata", "true")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	return body, nil
}

func (r *Reader) readAndTrimFile(filename string) (string, error) {
	r.logger.Debug("Reading file", "filename", filename)
	content, err := os.ReadFile(filename)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(content)), nil
}
