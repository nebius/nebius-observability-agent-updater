package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Response struct {
	StatusMsg     string                 `json:"status"`
	UpSince       time.Time              `json:"upSince"`
	Uptime        string                 `json:"uptime"`
	Reasons       []string               `json:"reasons"`
	CheckStatuses map[string]CheckStatus `json:"moduleStatuses"`
}

type CheckStatus struct {
	IsOk       bool        `json:"isOk"`
	Reasons    []string    `json:"reasons"`
	Parameters []Parameter `json:"parameters"`
}

type Parameter struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func makeErrorResponse(message string) Response {
	return Response{
		StatusMsg: "error",
		Reasons:   []string{message},
		CheckStatuses: map[string]CheckStatus{
			"process": {
				IsOk:    false,
				Reasons: []string{message},
			},
		},
	}
}

func CheckHealthWithReasons(endpoint string) (bool, Response) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, makeErrorResponse(fmt.Sprintf("error creating request to %s: %v", endpoint, err))
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, makeErrorResponse(fmt.Sprintf("error making request to %s: %v", endpoint, err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, makeErrorResponse(fmt.Sprintf("error reading response body from %s: %v", endpoint, err))
	}

	var result Response
	err = json.Unmarshal(body, &result)
	if err != nil {
		return false, makeErrorResponse(fmt.Sprintf("error unmarshaling JSON from %s: %v", endpoint, err))
	}

	healthy := resp.StatusCode == http.StatusOK

	return healthy, result
}
