package api

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai"
)

// visionMux wires the vision endpoint (only) so tests stay hermetic —
// no agent, no RAG, no full router graph.
func visionMux(t *testing.T, client *ai.Client) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	registerVisionRoutes(mux, client)
	return mux
}

// writeImagePart attaches one "image" file field with a specific
// Content-Type header so we can test media-type validation. The
// default multipart writer infers from filename extension which isn't
// enough for our whitelist check.
func writeImagePart(t *testing.T, mw *multipart.Writer, filename, mediaType string, data []byte) {
	t.Helper()
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="image"; filename="`+filename+`"`)
	h.Set("Content-Type", mediaType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("CreatePart: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("part.Write: %v", err)
	}
}

func buildVisionBody(t *testing.T, tool string, images []struct {
	name      string
	mediaType string
	data      []byte
}) (*bytes.Buffer, string) {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("tool", tool)
	for _, img := range images {
		writeImagePart(t, mw, img.name, img.mediaType, img.data)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return body, mw.FormDataContentType()
}

func TestVision_RejectsWrongContentType(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/ai/topology/image", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("want 415, got %d", resp.StatusCode)
	}
}

func TestVision_RejectsMissingTool(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body, ct := buildVisionBody(t, "", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"a.png", "image/png", []byte{0x89, 0x50}}})
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestVision_RejectsUnknownMediaType(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body, ct := buildVisionBody(t, "terraform", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"a.svg", "image/svg+xml", []byte("<svg/>")}})
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "unsupported image type") {
		t.Errorf("missing 'unsupported image type' in %q", msg)
	}
}

func TestVision_RejectsMissingImage(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("tool", "terraform")
	_ = mw.Close()
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", mw.FormDataContentType(), body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestVision_NonVisionProviderReturns400(t *testing.T) {
	// Default Ollama client doesn't implement VisionUser.
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body, ct := buildVisionBody(t, "terraform", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"a.png", "image/png", []byte{0x89, 0x50}}})
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for non-vision provider, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "does not support vision") {
		t.Errorf("expected vision hint, got %q", msg)
	}
}

func TestVision_RejectsTooManyImages(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	var imgs []struct {
		name      string
		mediaType string
		data      []byte
	}
	for i := 0; i < maxImagesPerRequest+1; i++ {
		imgs = append(imgs, struct {
			name      string
			mediaType string
			data      []byte
		}{"a.png", "image/png", []byte{0x89, 0x50}})
	}
	body, ct := buildVisionBody(t, "terraform", imgs)
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for >%d images, got %d", maxImagesPerRequest, resp.StatusCode)
	}
}

func TestVision_AcceptsContentTypeWithParametersAndCase(t *testing.T) {
	// "Image/JPEG; charset=binary" must be accepted — parseMediaType
	// strips the parameter and lowercases the type. The underlying
	// provider still rejects (non-vision Ollama), so we assert the
	// status is 400 AND the body carries the vision-provider hint.
	// The positive match matters — a "unsupported image type" false
	// negative would've slipped past a body-only negative assertion.
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body, ct := buildVisionBody(t, "terraform", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"diag.jpeg", "Image/JPEG; charset=binary", []byte{0xFF, 0xD8, 0xFF}}})
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	msg, _ := io.ReadAll(resp.Body)
	bodyMsg := string(msg)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for non-vision provider, got %d with body %q", resp.StatusCode, bodyMsg)
	}
	if !strings.Contains(bodyMsg, "does not support vision") {
		t.Errorf("want 'does not support vision' hint, got %q", bodyMsg)
	}
}

func TestVision_RejectsEmptyImageUpload(t *testing.T) {
	// A zero-byte image with a valid Content-Type used to pass the
	// handler's len(images)>0 gate but then get silently dropped by
	// the provider's MediaType/Data filter, running the model text-
	// only. The boundary check now rejects empty payloads explicitly.
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	body, ct := buildVisionBody(t, "terraform", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"empty.png", "image/png", []byte{}}})
	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for empty upload, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "empty") {
		t.Errorf("want 'empty' hint, got %q", msg)
	}
}

func TestVision_RejectsOversizedImage(t *testing.T) {
	srv := httptest.NewServer(visionMux(t, ai.NewClient("http://127.0.0.1:1", "x")))
	defer srv.Close()

	// Build a payload whose single image is one byte over the per-image limit.
	oversized := make([]byte, maxImageBytes+1)
	body, ct := buildVisionBody(t, "terraform", []struct {
		name      string
		mediaType string
		data      []byte
	}{{"big.png", "image/png", oversized}})

	resp, err := http.Post(srv.URL+"/api/ai/topology/image", ct, body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for oversized image, got %d", resp.StatusCode)
	}
	msg, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(msg), "exceeds") {
		t.Errorf("want 'exceeds' in error message, got %q", string(msg))
	}
}
