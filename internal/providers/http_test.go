package providers

import (
	"bytes"
	"context"
	"fmt"
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

func TestProbeDownloadFallsBackToRangeGET(t *testing.T) {
	var methods []string
	client := NewHTTPClient(5*time.Second, 0, nil)
	client.Client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		methods = append(methods, req.Method)
		if req.Method == http.MethodHead {
			return &http.Response{
				StatusCode: http.StatusForbidden,
				Status:     "403 Forbidden",
				Body:       io.NopCloser(bytes.NewReader(nil)),
				Request:    req,
			}, nil
		}
		if req.Method != http.MethodGet {
			return nil, fmt.Errorf("unexpected method %s", req.Method)
		}
		if req.Header.Get("Range") != "bytes=0-0" {
			return nil, fmt.Errorf("range header = %q", req.Header.Get("Range"))
		}
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			Status:        "206 Partial Content",
			Header:        http.Header{"Content-Range": []string{"bytes 0-0/12345"}},
			ContentLength: 1,
			Body:          io.NopCloser(bytes.NewReader(nil)),
			Request:       req,
		}, nil
	})

	probe, err := client.ProbeDownload(context.Background(), "https://example.com/image.iso")
	if err != nil {
		t.Fatal(err)
	}
	if len(methods) != 2 || methods[0] != http.MethodHead || methods[1] != http.MethodGet {
		t.Fatalf("methods = %#v", methods)
	}
	if probe.Method != http.MethodGet {
		t.Fatalf("probe method = %s", probe.Method)
	}
	if probe.StatusCode != http.StatusPartialContent {
		t.Fatalf("status = %d", probe.StatusCode)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
