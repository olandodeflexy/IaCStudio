package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/iac-studio/iac-studio/internal/ai"
)

// maxImageBytes caps a single image at 8MB and the whole multipart
// payload at 20MB — matches Anthropic's documented limits (8MB per
// image, 20MB per request) so we reject oversize uploads before
// burning a round-trip.
const (
	maxImageBytes      int64 = 8 * 1024 * 1024
	maxVisionReqBytes  int64 = 20 * 1024 * 1024
	maxImagesPerRequest      = 5
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
		if !strings.HasPrefix(ct, "multipart/form-data") {
			http.Error(w, "expected multipart/form-data", http.StatusUnsupportedMediaType)
			return
		}
		if err := r.ParseMultipartForm(maxVisionReqBytes); err != nil {
			http.Error(w, "invalid multipart body: "+err.Error(), http.StatusBadRequest)
			return
		}
		// ParseMultipartForm spills to tmp files above its 32MB default
		// in-memory cap. RemoveAll cleans those up when the handler
		// returns — without it, aborted uploads would leave temp files
		// behind for the lifetime of the process.
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
			status := http.StatusBadGateway
			// The bridge surfaces a descriptive error when the provider
			// isn't vision-capable — that's a 400 (client picked the
			// wrong provider), not a 502.
			if strings.Contains(err.Error(), "does not support vision") {
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
// The file reader is bounded with io.LimitReader so a client claiming
// a small Content-Length can't sneak past by streaming more bytes than
// declared.
func readUploadedImages(r *http.Request) ([]ai.DiagramImage, error) {
	files := r.MultipartForm.File["image"]
	if len(files) > maxImagesPerRequest {
		return nil, fmt.Errorf("too many images: %d (max %d)", len(files), maxImagesPerRequest)
	}
	out := make([]ai.DiagramImage, 0, len(files))
	for _, fh := range files {
		mediaType := fh.Header.Get("Content-Type")
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
		out = append(out, ai.DiagramImage{MediaType: mediaType, Data: data})
	}
	return out, nil
}
