package providers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"ventoy-update/internal/logging"
)

const UserAgent = "ventoy-update/0.1 (+https://github.com/local/ventoy-update)"

type HTTPClient struct {
	Client  *http.Client
	Retries int
	Logger  *logging.Logger
}

func NewHTTPClient(timeout time.Duration, retries int, logger *logging.Logger) *HTTPClient {
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	if retries < 0 {
		retries = 0
	}
	return &HTTPClient{
		Client: &http.Client{
			Timeout: timeout,
		},
		Retries: retries,
		Logger:  logger,
	}
}

func (c *HTTPClient) GetBytes(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	resp, err := c.Do(ctx, http.MethodGet, rawURL, nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var reader io.Reader = resp.Body
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, fmt.Errorf("response from %s exceeded %d bytes", rawURL, limit)
	}
	return data, nil
}

func (c *HTTPClient) GetText(ctx context.Context, rawURL string, limit int64) (string, error) {
	data, err := c.GetBytes(ctx, rawURL, limit)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (c *HTTPClient) Head(ctx context.Context, rawURL string) (*http.Response, error) {
	return c.Do(ctx, http.MethodHead, rawURL, nil, nil)
}

func (c *HTTPClient) Do(ctx context.Context, method, rawURL string, headers map[string]string, body []byte) (*http.Response, error) {
	if c.Client == nil {
		c.Client = &http.Client{Timeout: 30 * time.Second}
	}
	var lastErr error
	attempts := c.Retries + 1
	for attempt := 1; attempt <= attempts; attempt++ {
		if c.Logger != nil {
			c.Logger.Info("request", logging.Fields{
				"method":  method,
				"url":     rawURL,
				"attempt": attempt,
			})
		}

		var bodyReader io.Reader
		if body != nil {
			bodyReader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
		if err != nil {
			return nil, err
		}
		req.Header.Set("User-Agent", UserAgent)
		req.Header.Set("Accept", "*/*")
		for key, value := range headers {
			req.Header.Set(key, value)
		}

		resp, err := c.Client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		if err == nil {
			lastErr = fmt.Errorf("%s %s returned %s", method, rawURL, resp.Status)
			io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
				break
			}
		} else {
			lastErr = err
		}
		if attempt < attempts {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, lastErr
}
