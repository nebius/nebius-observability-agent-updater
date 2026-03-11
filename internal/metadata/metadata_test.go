package metadata

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	instanceDataPath   = "/v1/instance-data"
	tokenAccessPath    = "/v1/iam/tsa/token/access_token"
	tokenExpiresAtPath = "/v1/iam/tsa/token/expires_at"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, nil))
}

func TestGetParentId_IMDS(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.Header.Get("Metadata"))
		if r.URL.Path == instanceDataPath {
			_, err := w.Write([]byte(`{"id": "inst-123", "parent_id": "parent-456"}`))
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
	}, testLogger())

	parentId, err := reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-456", parentId)
}

func TestGetInstanceId_FromFile(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "instance-id"), []byte("inst-from-file\n"), 0644)
	require.NoError(t, err)

	reader := NewReader(Config{
		UseMetadataService: true,
		Path:               tmpDir,
		InstanceIdFilename: "instance-id",
	}, testLogger())

	instanceId, isFallback, err := reader.GetInstanceId()
	require.NoError(t, err)
	assert.Equal(t, "inst-from-file", instanceId)
	assert.False(t, isFallback)
}

func TestGetInstanceId_IMDSFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == instanceDataPath {
			_, err := w.Write([]byte(`{"id": "inst-from-imds", "parent_id": "parent-456"}`))
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	// No file exists — falls back to IMDS
	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		Path:                       t.TempDir(),
		InstanceIdFilename:         "instance-id",
	}, testLogger())

	instanceId, isFallback, err := reader.GetInstanceId()
	require.NoError(t, err)
	assert.Equal(t, "inst-from-imds", instanceId)
	assert.True(t, isFallback)
}

func TestGetInstanceId_IMDSFallbackURL(t *testing.T) {
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == instanceDataPath {
			_, err := w.Write([]byte(`{"id": "inst-fallback", "parent_id": "parent-fallback"}`))
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer fallbackServer.Close()

	// No file exists, primary IMDS unreachable — falls back to IMDS fallback URL
	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         "http://127.0.0.1:1", // unreachable
		MetadataServiceFallbackURL: fallbackServer.URL,
		Path:                       t.TempDir(),
		InstanceIdFilename:         "instance-id",
	}, testLogger())

	instanceId, isFallback, err := reader.GetInstanceId()
	require.NoError(t, err)
	assert.Equal(t, "inst-fallback", instanceId)
	assert.True(t, isFallback)
}

func TestGetIamToken_IMDS(t *testing.T) {
	expiresAt := time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.Header.Get("Metadata"))
		switch r.URL.Path {
		case tokenAccessPath:
			_, err := w.Write([]byte("my-iam-token"))
			assert.NoError(t, err)
		case tokenExpiresAtPath:
			_, err := w.Write([]byte(expiresAt))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		MetadataTokenType:          "tsa",
	}, testLogger())

	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "my-iam-token", token)
}

func TestGetIamToken_Cached(t *testing.T) {
	tokenCallCount := 0
	expiresAt := time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenAccessPath:
			tokenCallCount++
			_, err := w.Write([]byte("my-iam-token"))
			assert.NoError(t, err)
		case tokenExpiresAtPath:
			_, err := w.Write([]byte(expiresAt))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		MetadataTokenType:          "tsa",
	}, testLogger())

	// First call fetches from IMDS
	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "my-iam-token", token)

	// Second call should use cache
	token, err = reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "my-iam-token", token)

	assert.Equal(t, 1, tokenCallCount, "token should be fetched only once while cached")
}

func TestGetIamToken_RefreshesWhenNearExpiry(t *testing.T) {
	tokenCallCount := 0
	expiresAt := time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenAccessPath:
			tokenCallCount++
			_, err := fmt.Fprintf(w, "token-%d", tokenCallCount)
			assert.NoError(t, err)
		case tokenExpiresAtPath:
			_, err := w.Write([]byte(expiresAt))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		MetadataTokenType:          "tsa",
	}, testLogger())

	// First fetch
	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "token-1", token)
	assert.Equal(t, 1, tokenCallCount)

	// Simulate token about to expire (within refresh margin)
	reader.tokenMu.Lock()
	reader.cachedIAM.expiresAt = time.Now().Add(30 * time.Minute) // less than 1 hour margin
	reader.tokenMu.Unlock()

	// Should re-fetch
	token, err = reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "token-2", token)
	assert.Equal(t, 2, tokenCallCount)
}

func TestGetIamToken_UsesStaleTokenOnRefreshFailure(t *testing.T) {
	tokenCallCount := 0
	expiresAt := time.Now().Add(12 * time.Hour).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenAccessPath:
			tokenCallCount++
			if tokenCallCount > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, err := w.Write([]byte("original-token"))
			assert.NoError(t, err)
		case tokenExpiresAtPath:
			_, err := w.Write([]byte(expiresAt))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		MetadataTokenType:          "tsa",
	}, testLogger())

	// First fetch succeeds
	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "original-token", token)

	// Simulate near expiry but not yet expired
	reader.tokenMu.Lock()
	reader.cachedIAM.expiresAt = time.Now().Add(30 * time.Minute) // needs refresh but not expired
	reader.tokenMu.Unlock()

	// Refresh fails — should return stale token since it hasn't expired yet
	token, err = reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "original-token", token)
}

func TestGetIamToken_AlreadyExpired(t *testing.T) {
	expiredAt := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339Nano)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case tokenAccessPath:
			_, err := w.Write([]byte("expired-token"))
			assert.NoError(t, err)
		case tokenExpiresAtPath:
			_, err := w.Write([]byte(expiredAt))
			assert.NoError(t, err)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "tsa-token"), []byte("file-token\n"), 0644)
	require.NoError(t, err)

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		MetadataTokenType:          "tsa",
		Path:                       tmpDir,
		IamTokenFilename:           "tsa-token",
	}, testLogger())

	// Token from IMDS is expired — should error from getCachedIAMToken and fall back to file
	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "file-token", token)

	// Verify token was not cached
	reader.tokenMu.Lock()
	assert.Nil(t, reader.cachedIAM, "expired token should not be cached")
	reader.tokenMu.Unlock()
}

func TestGetIamToken_FileFallback(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "tsa-token"), []byte("file-token\n"), 0644)
	require.NoError(t, err)

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         "http://127.0.0.1:1",
		MetadataServiceFallbackURL: "http://127.0.0.1:1",
		Path:                       tmpDir,
		IamTokenFilename:           "tsa-token",
		MetadataTokenType:          "tsa",
	}, testLogger())

	token, err := reader.GetIamToken()
	require.NoError(t, err)
	assert.Equal(t, "file-token", token)
}

func TestGetInstanceData_Cached(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == instanceDataPath {
			callCount++
			_, err := w.Write([]byte(`{"id": "inst-123", "parent_id": "parent-456"}`))
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
	}, testLogger())

	// Call GetParentId twice - should only hit the server once
	parentId, err := reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-456", parentId)

	parentId, err = reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-456", parentId)

	assert.Equal(t, 1, callCount, "instance-data should be fetched only once within TTL")
}

func TestGetInstanceData_RefreshesAfterTTL(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == instanceDataPath {
			callCount++
			_, err := fmt.Fprintf(w, `{"id": "inst-%d", "parent_id": "parent-%d"}`, callCount, callCount)
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
	}, testLogger())

	// First fetch
	parentId, err := reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-1", parentId)
	assert.Equal(t, 1, callCount)

	// Expire the cache
	reader.mu.Lock()
	reader.cachedFetchedAt = time.Now().Add(-instanceDataCacheTTL - time.Second)
	reader.mu.Unlock()

	// Should re-fetch
	parentId, err = reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-2", parentId)
	assert.Equal(t, 2, callCount)
}

func TestGetInstanceData_StaleCache_OnRefreshFailure(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == instanceDataPath {
			callCount++
			if callCount > 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, err := w.Write([]byte(`{"id": "inst-original", "parent_id": "parent-original"}`))
			assert.NoError(t, err)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
	}, testLogger())

	// First fetch succeeds
	parentId, err := reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-original", parentId)

	// Expire the cache
	reader.mu.Lock()
	reader.cachedFetchedAt = time.Now().Add(-instanceDataCacheTTL - time.Second)
	reader.mu.Unlock()

	// Refresh fails — should return stale cached data
	parentId, err = reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-original", parentId)
}

func TestGetInstanceId_MetadataServiceDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "instance-id"), []byte("inst-from-file\n"), 0644)
	require.NoError(t, err)

	reader := NewReader(Config{
		UseMetadataService: false,
		Path:               tmpDir,
		InstanceIdFilename: "instance-id",
	}, testLogger())

	instanceId, isFallback, err := reader.GetInstanceId()
	require.NoError(t, err)
	assert.Equal(t, "inst-from-file", instanceId)
	assert.False(t, isFallback)
}

func TestIMDS_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	err := os.WriteFile(filepath.Join(tmpDir, "parent-id"), []byte("parent-from-file\n"), 0644)
	require.NoError(t, err)

	reader := NewReader(Config{
		UseMetadataService:         true,
		MetadataServiceURL:         server.URL,
		MetadataServiceFallbackURL: server.URL,
		Path:                       tmpDir,
		ParentIdFilename:           "parent-id",
	}, testLogger())

	parentId, err := reader.GetParentId()
	require.NoError(t, err)
	assert.Equal(t, "parent-from-file", parentId)
}
