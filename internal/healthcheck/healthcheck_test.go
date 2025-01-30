package healthcheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// nolint: gocognit
func TestCheckHealthWithReasons(t *testing.T) {
	tests := []struct {
		name           string
		serverResponse Response
		statusCode     int
		wantHealthy    bool
		wantResponse   Response
	}{
		{
			name: "Healthy response with all modules",
			serverResponse: Response{
				StatusMsg: "healthy",
				UpSince:   time.Now(),
				Uptime:    "1h",
				Reasons:   []string{"All systems operational"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    true,
						Reasons: []string{"Process running normally"},
					},
					"cpu": {
						IsOk:    true,
						Reasons: []string{"CPU pipeline operational"},
					},
					"gpu": {
						IsOk:    true,
						Reasons: []string{"GPU pipeline operational"},
					},
				},
			},
			statusCode:  http.StatusOK,
			wantHealthy: true,
			wantResponse: Response{
				StatusMsg: "healthy",
				Reasons:   []string{"All systems operational"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    true,
						Reasons: []string{"Process running normally"},
					},
					"cpu": {
						IsOk:    true,
						Reasons: []string{"CPU pipeline operational"},
					},
					"gpu": {
						IsOk:    true,
						Reasons: []string{"GPU pipeline operational"},
					},
				},
			},
		},
		{
			name: "Mixed health status with some modules failing",
			serverResponse: Response{
				StatusMsg: "error",
				UpSince:   time.Now(),
				Uptime:    "1h",
				Reasons:   []string{"GPU pipeline error"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    true,
						Reasons: []string{"Process running normally"},
					},
					"cpu": {
						IsOk:    true,
						Reasons: []string{"CPU pipeline operational"},
					},
					"gpu": {
						IsOk:    false,
						Reasons: []string{"GPU pipeline error: CUDA initialization failed"},
					},
				},
			},
			statusCode:  http.StatusInternalServerError,
			wantHealthy: false,
			wantResponse: Response{
				StatusMsg: "error",
				Reasons:   []string{"GPU pipeline error"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    true,
						Reasons: []string{"Process running normally"},
					},
					"cpu": {
						IsOk:    true,
						Reasons: []string{"CPU pipeline operational"},
					},
					"gpu": {
						IsOk:    false,
						Reasons: []string{"GPU pipeline error: CUDA initialization failed"},
					},
				},
			},
		},
		{
			name: "All modules unhealthy",
			serverResponse: Response{
				StatusMsg: "error",
				UpSince:   time.Now(),
				Uptime:    "1h",
				Reasons:   []string{"Database connection error", "High CPU usage"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    false,
						Reasons: []string{"Database connection error", "High CPU usage"},
					},
				},
			},
			statusCode:  http.StatusInternalServerError,
			wantHealthy: false,
			wantResponse: Response{
				StatusMsg: "error",
				Reasons:   []string{"Database connection error", "High CPU usage"},
				CheckStatuses: map[string]CheckStatus{
					"process": {
						IsOk:    false,
						Reasons: []string{"Database connection error", "High CPU usage"},
					},
				},
			},
		},
		{
			name: "Empty response",
			serverResponse: Response{
				StatusMsg:     "",
				CheckStatuses: map[string]CheckStatus{},
			},
			statusCode:  http.StatusOK,
			wantHealthy: true,
			wantResponse: Response{
				StatusMsg:     "",
				CheckStatuses: map[string]CheckStatus{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.statusCode)
				_ = json.NewEncoder(w).Encode(tt.serverResponse)
			}))
			defer server.Close()

			healthy, resp := CheckHealthWithReasons(server.URL)

			if healthy != tt.wantHealthy {
				t.Errorf("CheckHealthWithReasons() healthy = %v, want %v", healthy, tt.wantHealthy)
			}

			// Compare relevant fields from the response
			if resp.StatusMsg != tt.wantResponse.StatusMsg {
				t.Errorf("CheckHealthWithReasons() status = %v, want %v", resp.StatusMsg, tt.wantResponse.StatusMsg)
			}

			if len(resp.Reasons) != len(tt.wantResponse.Reasons) {
				t.Errorf("CheckHealthWithReasons() reasons length = %d, want %d", len(resp.Reasons), len(tt.wantResponse.Reasons))
			}

			for i, reason := range resp.Reasons {
				if i < len(tt.wantResponse.Reasons) && reason != tt.wantResponse.Reasons[i] {
					t.Errorf("CheckHealthWithReasons() reason[%d] = %s, want %s", i, reason, tt.wantResponse.Reasons[i])
				}
			}

			// Compare CheckStatuses
			if len(resp.CheckStatuses) != len(tt.wantResponse.CheckStatuses) {
				t.Errorf("CheckHealthWithReasons() checkStatuses length = %d, want %d",
					len(resp.CheckStatuses), len(tt.wantResponse.CheckStatuses))
			}

			for k, v := range resp.CheckStatuses {
				wantStatus, exists := tt.wantResponse.CheckStatuses[k]
				if !exists {
					t.Errorf("CheckHealthWithReasons() unexpected checkStatus key %s", k)
					continue
				}
				if v.IsOk != wantStatus.IsOk {
					t.Errorf("CheckHealthWithReasons() checkStatus[%s].IsOk = %v, want %v", k, v.IsOk, wantStatus.IsOk)
				}

				// Compare reasons
				if len(v.Reasons) != len(wantStatus.Reasons) {
					t.Errorf("CheckHealthWithReasons() checkStatus[%s].Reasons length = %d, want %d",
						k, len(v.Reasons), len(wantStatus.Reasons))
					continue
				}
				for i, reason := range v.Reasons {
					if reason != wantStatus.Reasons[i] {
						t.Errorf("CheckHealthWithReasons() checkStatus[%s].Reasons[%d] = %s, want %s",
							k, i, reason, wantStatus.Reasons[i])
					}
				}

				// Compare parameters if present
				if len(v.Parameters) != len(wantStatus.Parameters) {
					t.Errorf("CheckHealthWithReasons() checkStatus[%s].Parameters length = %d, want %d",
						k, len(v.Parameters), len(wantStatus.Parameters))
					continue
				}
				for i, param := range v.Parameters {
					wantParam := wantStatus.Parameters[i]
					if param.Name != wantParam.Name || param.Value != wantParam.Value {
						t.Errorf("CheckHealthWithReasons() checkStatus[%s].Parameters[%d] = {%s: %s}, want {%s: %s}",
							k, i, param.Name, param.Value, wantParam.Name, wantParam.Value)
					}
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
		wantError   string
	}{
		{
			name: "Server timeout",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					time.Sleep(6 * time.Second)
				}))
			},
			wantHealthy: false,
			wantError:   "error making request to",
		},
		{
			name: "Invalid JSON response",
			serverSetup: func() *httptest.Server {
				return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_, _ = w.Write([]byte("invalid json"))
				}))
			},
			wantHealthy: false,
			wantError:   "error unmarshaling JSON from",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := tt.serverSetup()
			defer server.Close()

			healthy, resp := CheckHealthWithReasons(server.URL)

			if healthy != tt.wantHealthy {
				t.Errorf("CheckHealthWithReasons() healthy = %v, want %v", healthy, tt.wantHealthy)
			}

			if len(resp.Reasons) == 0 || !strings.Contains(resp.Reasons[0], tt.wantError) {
				t.Errorf("CheckHealthWithReasons() error = %v, want it to contain %s", resp.Reasons, tt.wantError)
			}

			// Verify error response structure
			if resp.StatusMsg != "error" {
				t.Errorf("CheckHealthWithReasons() status = %v, want 'error'", resp.StatusMsg)
			}

			if len(resp.CheckStatuses) != 1 || resp.CheckStatuses["process"].IsOk {
				t.Errorf("CheckHealthWithReasons() invalid error response structure")
			}
		})
	}
}
