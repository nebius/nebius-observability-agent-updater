package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type CheckStatus struct {
	IsOk       bool        `json:"isOk"`
	Reasons    []string    `json:"reasons"`
	Parameters []Parameter `json:"parameters"`
}

type HealthResponse struct {
	StatusMsg     string                 `json:"status"`
	UpSince       time.Time              `json:"upSince"`
	Uptime        string                 `json:"uptime"`
	Reasons       []string               `json:"reasons"`
	CheckStatuses map[string]CheckStatus `json:"moduleStatuses"`
}

var startTime = time.Now()

func healthHandler(w http.ResponseWriter, r *http.Request) {
	uptime := time.Since(startTime)
	resp := HealthResponse{
		StatusMsg: "ok",
		UpSince:   startTime,
		Uptime:    uptime.String(),
		Reasons:   []string{},
		CheckStatuses: map[string]CheckStatus{
			"process": {
				IsOk:    true,
				Reasons: []string{},
				Parameters: []Parameter{
					{Name: "pid", Value: fmt.Sprintf("%d", 1)},
				},
			},
			"cpu": {
				IsOk:    true,
				Reasons: []string{},
			},
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/health", healthHandler)
	fmt.Println("fake-agent listening on :54783")
	if err := http.ListenAndServe(":54783", nil); err != nil {
		fmt.Printf("failed to start: %v\n", err)
	}
}
