package healthcheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCheckHealthWithReasons(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse response
		statusCode     int
		wantHealthy    bool
		wantReasons    []string
	}{
		{
			name: "Healthy response",
			serverResponse: response{
				Status:  "healthy",
				Reasons: []string{"All systems operational"},
			},
			statusCode:  http.StatusOK,
			wantHealthy: true,
			wantReasons: []string{"All systems operational"},
		},
		{
			name: "Unhealthy response",
			serverResponse: response{
				Status:  "unhealthy",
				Reasons: []string{"Database connection error", "High CPU usage"},
			},
			statusCode:  http.StatusInternalServerError,
			wantHealthy: false,
			wantReasons: []string{"Database connection error", "High CPU usage"},
		},
		{
			name: "Empty response",
			serverResponse: response{
				Status:  "",
				Reasons: []string{},
			},
			statusCode:  http.StatusOK,
			wantHealthy: true,
			wantReasons: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				json.NewEncoder(w).Encode(tt.serverResponse)
			}))
			defer server.Close()

			healthy, reasons := CheckHealthWithReasons(server.URL)

			if healthy != tt.wantHealthy {
				t.Errorf("CheckHealthWithReasons() healthy = %v, want %v", healthy, tt.wantHealthy)
			}

			if len(reasons) != len(tt.wantReasons) {
				t.Errorf("CheckHealthWithReasons() reasons length = %d, want %d", len(reasons), len(tt.wantReasons))
			}

			for i, reason := range reasons {
				if reason != tt.wantReasons[i] {
					t.Errorf("CheckHealthWithReasons() reason[%d] = %s, want %s", i, reason, tt.wantReasons[i])
				}
			}
		})
	}
}

func TestCheckHealthWithReasons_ServerErrors(t *testing.T) {
	tests := []struct {
		name        string
		serverSetup func() *httptest.Server
		wantHealthy bool
		wantReason  string
	}{
		{
			name: "Server timeout",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(6 * time.Second)
				}))
			},
			wantHealthy: false,
			wantReason:  "error making request to",
		},
		{
			name: "Invalid JSON response",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Write([]byte("invalid json"))
				}))
			},
			wantHealthy: false,
			wantReason:  "error unmarshaling JSON from",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverSetup()
			defer server.Close()

			healthy, reasons := CheckHealthWithReasons(server.URL)

			if healthy != tt.wantHealthy {
				t.Errorf("CheckHealthWithReasons() healthy = %v, want %v", healthy, tt.wantHealthy)
			}

			if len(reasons) == 0 || !strings.Contains(reasons[0], tt.wantReason) {
				t.Errorf("CheckHealthWithReasons() reason = %v, want it to contain %s", reasons, tt.wantReason)
			}
		})
	}
}
