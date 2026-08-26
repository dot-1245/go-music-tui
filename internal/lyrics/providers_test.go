package lyrics

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

func TestFetchProviderKeepsHTTPExchange(t *testing.T) {
	body := []byte(`{"track":"Test Track","artist":"Test Artist","duration":120,"type":"karaoke","lyrics":"[00:00.00]<00:00.00>Hello <00:00.50>world"}`)
	client := NewClient(&http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/lyrics" {
			t.Fatalf("unexpected request path: %s", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}, nil)

	report := client.FetchProvider(context.Background(), "synclrc", NewRequest("Test Track", []string{"Test Artist"}, "Test Artist", "Test Album", 120))
	if report.Result == nil || report.Result.Source != "synclrc-enhanced" {
		t.Fatalf("unexpected provider result: %#v", report.Result)
	}
	if len(report.Requests) != 1 || report.Requests[0].StatusCode != http.StatusOK {
		t.Fatalf("unexpected request report: %#v", report.Requests)
	}
	if !bytes.Equal(report.Requests[0].Body, body) {
		t.Fatalf("request body was not retained: %q", report.Requests[0].Body)
	}
}

func TestFetchProviderCanAvoidRetainingBodies(t *testing.T) {
	body := []byte(`{"track":"Test Track","artist":"Test Artist","lyrics":"[00:00]lyrics"}`)
	client := NewClientWithOptions(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}, nil, ClientOptions{MaxResponseBytes: 1024, CaptureBody: false})
	report := client.FetchProvider(context.Background(), "synclrc", NewRequest("Test Track", []string{"Test Artist"}, "Test Artist", "", 0))
	if report.Result == nil || len(report.Requests) != 1 {
		t.Fatalf("unexpected report: %#v", report)
	}
	if len(report.Requests[0].Body) != 0 {
		t.Fatalf("response body was retained despite CaptureBody=false: %d bytes", len(report.Requests[0].Body))
	}
}

func TestInvalidJSONRetainsRawBodyForDiagnostics(t *testing.T) {
	body := []byte("not json")
	client := NewClientWithOptions(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body)), Header: make(http.Header)}, nil
	})}, nil, ClientOptions{MaxResponseBytes: -1, CaptureBody: true})
	report := client.FetchProvider(context.Background(), "synclrc", NewRequest("Test", []string{"Artist"}, "Artist", "", 0))
	if len(report.Requests) != 1 || report.Requests[0].Err == nil || !bytes.Equal(report.Requests[0].Body, body) {
		t.Fatalf("invalid JSON diagnostic body was lost: %#v", report.Requests)
	}
}

func TestPickBestMatchAcceptsIntegerDuration(t *testing.T) {
	best := PickBestMatch([]map[string]interface{}{
		{"trackName": "Track", "artistName": "Artist", "duration": int(120), "syncedLyrics": "[00:00]lyrics"},
		{"trackName": "Track", "artistName": "Artist", "duration": int(124), "syncedLyrics": "[00:00]lyrics"},
	}, 120, "Track", []string{"Artist"}, nil)
	if best == nil || best["duration"] != int(120) {
		t.Fatalf("unexpected best candidate: %#v", best)
	}
}

func TestPickBestMatchAcceptsMissingDuration(t *testing.T) {
	best := PickBestMatch([]map[string]interface{}{
		{"trackName": "Track", "artistName": "Artist", "syncedLyrics": "[00:00]lyrics"},
	}, 120, "Track", []string{"Artist"}, nil)
	if best == nil {
		t.Fatal("candidate without duration was rejected")
	}
}

func TestStripLeadingLyricMetadataDoesNotMutateInput(t *testing.T) {
	original := ParseSyncedLyrics("[00:00]Track (Artist)\n[00:03]Real lyric")
	stripped := StripLeadingLyricMetadata(original, "Track", []string{"Artist"})
	if len(stripped) != 1 || stripped[0].Text != "Real lyric" {
		t.Fatalf("unexpected stripped result: %#v", stripped)
	}
	if len(original) != 2 || original[0].Text != "Track (Artist)" {
		t.Fatalf("input lines were mutated: %#v", original)
	}
}

func TestUnknownProviderIsReported(t *testing.T) {
	report := NewClient(nil, nil).FetchProvider(context.Background(), "unknown", Request{})
	if report.Result != nil || len(report.Requests) != 1 || report.Requests[0].Err == nil {
		t.Fatalf("unexpected unknown-provider report: %#v", report)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}
