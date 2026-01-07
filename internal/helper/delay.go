package helper

import (
	"context"
	"net/http"
	"time"
)

func GetUrlDelay(httpClient *http.Client, url string, timeout time.Duration) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return int(time.Since(start).Milliseconds()), nil
}
