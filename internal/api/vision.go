package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/iac-studio/iac-studio/internal/ai"
)

// maxImageBytes caps a single image at 8MB and the whole multipart
// payload at 20MB — matches Anthropic's documented limits (8MB per
// image, 20MB per request) so we reject oversize uploads before
// burning a round-trip.
//
// multipartMaxMemory is the small in-memory buffer ParseMultipartForm
// keeps before spilling to tmp files. 1MB is enough for the form
// fields (tool / provider / description) + part headers, which is all
// we want in RAM — uploaded images should spool to disk so we don't
// double-buffer (once by ParseMultipartForm, once by readUploadedImages
// into a new []byte per file).
const (
	maxImageBytes       int64 = 8 * 1024 * 1024
	maxVisionReqBytes   int64 = 20 * 1024 * 1024
	multipartMaxMemory  int64 = 1 * 1024 * 1024
	maxImagesPerRequest       = 5
)

// allowedImageMediaTypes mirrors the formats Anthropic's vision
// models accept. Kept as a set for O(1) membership; expand if/when a
// new provider adds support for something else.
var allowedImageMediaTypes = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/jpg":  {},
	"image/webp": {},
	"image/gif":  {},
}

// parseMediaType normalises a multipart part's Content-Type header:
// strips parameters (e.g. "image/jpeg; charset=binary" → "image/jpeg")
// and lowercases the result so the allow-list check is insensitive to
// casing and per-client variations. Returns the stripped+lowered
// media type, or an error when the header can't be parsed at all.
func parseMediaType(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("missing content-type")
	}
	mt, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return "", err
	}
	return strings.ToLower(mt), nil
}

// registerVisionRoutes wires the diagram-to-topology endpoint. Isolated
// so it can be mounted by the router and exercised by tests without
// pulling in the full request graph.
func registerVisionRoutes(mux *http.ServeMux, aiClient *ai.Client) {
	// POST /api/ai/topology/image
	// multipart/form-data with:
	//   tool        string (required, form field)
	//   provider    string (optional, default auto)
	//   description string (optional, extra context for the model)
	//   image       file(s) (required; repeatable up to maxImagesPerRequest)
	//
	// Response is synchronous — the model call blocks until the topology
	// comes back, then the handler writes the same {message, resources}
	// payload the text-topology WebSocket event carries. (Text topology
	// runs async with a 202 + WS result; vision runs sync because the
	// request itself is already a multipart upload and a second round-
	// trip to deliver the result adds no value.)
	//   { "message": "...", "resources": [...] }
	mux.HandleFunc("POST /api/ai/topology/image", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxVisionReqBytes)

		ct := r.Header.Get("Content-Type")
		mediaType, _, parseErr := mime.ParseMediaType(ct)
		if parseErr != nil || strings.ToLower(mediaType) != "multipart/form-data" {
			http.Error(w, "expected multipart/form-data", http.StatusUnsupportedMediaType)
			return
		}
		if err := r.ParseMultipartForm(multipartMaxMemory); err != nil {
			// MaxBytesReader reports the cap via *http.MaxBytesError
			// (Go 1.19+). errors.As is robust to wrapping so a future
			// middleware that re-wraps the error still routes to 413.
			var maxBytes *http.MaxBytesError
			if errors.As(err, &maxBytes) {
				http.Error(w, fmt.Sprintf("request body exceeds %dMB limit", maxBytes.Limit/1024/1024), http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid multipart body: "+err.Error(), http.StatusBadRequest)
			return
		}
		// ParseMultipartForm may create temporary files for file parts
		// that don't fit in multipartMaxMemory (and at 1MB we expect
		// every image to spool to disk). RemoveAll cleans those up when
		// the handler returns — without it, temp files would linger
		// until process exit.
		defer func() { _ = r.MultipartForm.RemoveAll() }()

		tool := strings.TrimSpace(r.FormValue("tool"))
		if tool == "" {
			http.Error(w, "tool field is required", http.StatusBadRequest)
			return
		}

		images, err := readUploadedImages(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if len(images) == 0 {
			http.Error(w, "at least one image upload is required", http.StatusBadRequest)
			return
		}

		msg, resources, err := aiClient.GenerateFromDiagram(r.Context(), ai.TopologyRequest{
			Description: strings.TrimSpace(r.FormValue("description")),
			Tool:        tool,
			Provider:    strings.TrimSpace(r.FormValue("provider")),
		}, images)
		if err != nil {
			// isClientError covers missing-API-key, unknown-provider,
			// wrong-provider-for-vision, and other config mistakes the
			// user can fix. Shared with the agent handler so the 400-vs-
			// 502 decision stays consistent across endpoints.
			status := http.StatusBadGateway
			if isClientError(err) {
				status = http.StatusBadRequest
			}
			http.Error(w, err.Error(), status)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"message":   msg,
			"resources": resources,
		})
	})
}

// readUploadedImages walks the multipart "image" fields and returns the
// DiagramImage slice. Rejects:
//   - media types outside the allow-list
//   - individual images over maxImageBytes
//   - more than maxImagesPerRequest uploads
//
// Each multipart file is read through io.LimitReader(maxImageBytes+1)
// so we enforce the per-image size cap while reading and can detect
// uploads that exceed maxImageBytes.
func readUploadedImages(r *http.Request) ([]ai.DiagramImage, error) {
	files := r.MultipartForm.File["image"]
	if len(files) > maxImagesPerRequest {
		return nil, fmt.Errorf("too many images: %d (max %d)", len(files), maxImagesPerRequest)
	}
	out := make([]ai.DiagramImage, 0, len(files))
	for _, fh := range files {
		ctHeader := fh.Header.Get("Content-Type")
		mediaType, err := parseMediaType(ctHeader)
		if err != nil {
			// Header was missing or malformed — distinct from "well-
			// formed but not one we support" so clients get the right
			// hint on what to fix.
			return nil, fmt.Errorf("image %q has missing or invalid Content-Type header: %w", fh.Filename, err)
		}
		if _, ok := allowedImageMediaTypes[mediaType]; !ok {
			return nil, fmt.Errorf("unsupported image type %q — use png, jpeg, webp, or gif", mediaType)
		}
		// Normalise image/jpg → image/jpeg so the provider sees the
		// canonical MIME string.
		if mediaType == "image/jpg" {
			mediaType = "image/jpeg"
		}
		f, err := fh.Open()
		if err != nil {
			return nil, fmt.Errorf("read uploaded image %q: %w", fh.Filename, err)
		}
		data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
		_ = f.Close()
		if err != nil {
			return nil, fmt.Errorf("read uploaded image %q: %w", fh.Filename, err)
		}
		if int64(len(data)) > maxImageBytes {
			return nil, fmt.Errorf("image %q exceeds %dMB limit", fh.Filename, maxImageBytes/1024/1024)
		}
		if len(data) == 0 {
			// Empty uploads would pass our len(images)>0 gate but get
			// silently dropped by the provider's MediaType/Data filter,
			// so the model runs text-only without warning the user.
			// Reject at the boundary so the endpoint reliably requires
			// a real image payload.
			return nil, fmt.Errorf("image %q is empty — upload must contain image data", fh.Filename)
		}
		out = append(out, ai.DiagramImage{MediaType: mediaType, Data: data})
	}
	return out, nil
}
