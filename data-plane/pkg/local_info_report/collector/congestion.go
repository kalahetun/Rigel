package collector

import (
	model "data-plane/pkg/local_info_report"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

func GetCongestionInfo() (model.ProxyStatus, error) {
	url := "http://127.0.0.1:8095/getCongestionInfo"

	p := model.ProxyStatus{}

	client := &http.Client{
		Timeout: 2 * time.Second,
	}

	resp, err := client.Get(url)
	if err != nil {
		return p, fmt.Errorf("request proxy status failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return p, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		return p, fmt.Errorf("decode proxy status failed: %w", err)
	}

	return p, nil
}
