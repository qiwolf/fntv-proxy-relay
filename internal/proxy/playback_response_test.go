package proxy

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type countingReadCloser struct {
	reader *strings.Reader
	reads  int
}

func newCountingReadCloser(value string) *countingReadCloser {
	return &countingReadCloser{reader: strings.NewReader(value)}
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	r.reads++
	return r.reader.Read(p)
}

func (r *countingReadCloser) Close() error { return nil }

func TestPlaybackInfoNon2xxPassesThroughWithoutReadingOrParsing(t *testing.T) {
	s := directServer(t, directConfig(t, "http://127.0.0.1:12345"))
	var observed playbackResponseObservation
	s.playbackObserver = func(v playbackResponseObservation) { observed = v }
	body := newCountingReadCloser("")
	req := newPlaybackRequest(http.MethodPost)
	req.ContentLength = 321
	resp := &http.Response{StatusCode: http.StatusBadRequest, ContentLength: 0, Header: make(http.Header), Body: body, Request: req}
	if err := s.handleResponse(resp); err != nil {
		t.Fatal(err)
	}
	if body.reads != 0 {
		t.Fatalf("non-2xx body was read %d times", body.reads)
	}
	if resp.StatusCode != http.StatusBadRequest || resp.Body != body {
		t.Fatal("non-2xx response was mutated")
	}
	if observed.StatusCode != http.StatusBadRequest || observed.RequestContentBytes != 321 || observed.BodyBytes != 0 || observed.Outcome != "passthrough_non_2xx" || observed.Elapsed <= 0 {
		t.Fatalf("observation = %+v", observed)
	}
}

func TestPlaybackInfoBlank2xxPassesThroughWithoutJSONParsing(t *testing.T) {
	s := directServer(t, directConfig(t, "http://127.0.0.1:12345"))
	var observed playbackResponseObservation
	s.playbackObserver = func(v playbackResponseObservation) { observed = v }
	req := newPlaybackRequest(http.MethodPost)
	resp := &http.Response{StatusCode: http.StatusOK, ContentLength: 3, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(" \n\t")), Request: req}
	if err := s.handleResponse(resp); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != " \n\t" || resp.StatusCode != http.StatusOK {
		t.Fatalf("blank response changed: status=%d body=%q", resp.StatusCode, got)
	}
	if observed.StatusCode != http.StatusOK || observed.BodyBytes != 3 || observed.Outcome != "passthrough_empty" || observed.Elapsed <= 0 {
		t.Fatalf("observation = %+v", observed)
	}
}

func TestPlaybackInfoValidBodyRecordsSanitizedObservation(t *testing.T) {
	s := directServer(t, directConfig(t, "http://127.0.0.1:12345"))
	var observed playbackResponseObservation
	s.playbackObserver = func(v playbackResponseObservation) { observed = v }
	body := `{"ItemId":"item-secret","MediaSources":[]}`
	resp := &http.Response{StatusCode: http.StatusOK, ContentLength: int64(len(body)), Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: newPlaybackRequest(http.MethodGet)}
	if err := s.handleResponse(resp); err != nil {
		t.Fatal(err)
	}
	if observed.StatusCode != http.StatusOK || observed.BodyBytes != int64(len(body)) || observed.Outcome != "inspected_nonempty" || observed.Elapsed <= 0 {
		t.Fatalf("observation = %+v", observed)
	}
}

func newPlaybackRequest(method string) *http.Request {
	req, _ := http.NewRequest(method, "http://proxy.invalid/Items/private-item/PlaybackInfo?api_key=secret", nil)
	return req.WithContext(context.WithValue(req.Context(), requestStartedKey{}, time.Now().Add(-10*time.Millisecond)))
}
