package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type response struct {
	Status  string   `json:"status"`
	Reasons []string `json:"Reasons"`
}

func CheckHealthWithReasons(endpoint string) (bool, []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, []string{fmt.Sprintf("error creating request to %s: %v", endpoint, err)}
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return false, []string{fmt.Sprintf("error making request to %s: %v", endpoint, err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, []string{fmt.Sprintf("error reading response body from %s: %v", endpoint, err)}
	}

	var result response
	err = json.Unmarshal(body, &result)
	if err != nil {
		return false, []string{fmt.Sprintf("error unmarshaling JSON from %s: %v", endpoint, err)}
	}

	healthy := resp.StatusCode == http.StatusOK

	if !healthy {
		result.Reasons = append(result.Reasons, fmt.Sprintf("unexpected status code %d", resp.StatusCode))
	}
	return healthy, result.Reasons
}
