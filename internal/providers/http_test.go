package providers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestProbeDownloadUsesHEADOnly(t *testing.T) {
	var method string
	client := NewHTTPClient(5*time.Second, 0, nil)
	client.Client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		method = req.Method
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        http.Header{"Content-Length": []string{"12345"}},
			ContentLength: 12345,
			Body:          io.NopCloser(bytes.NewReader(nil)),
			Request:       req,
		}, nil
	})

	probe, err := client.ProbeDownload(context.Background(), "https://example.com/image.iso")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodHead {
		t.Fatalf("method = %s", method)
	}
	if probe.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", probe.StatusCode)
	}
	if probe.ContentLength != 12345 {
		t.Fatalf("content length = %d", probe.ContentLength)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
