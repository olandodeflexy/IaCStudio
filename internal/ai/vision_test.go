package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/iac-studio/iac-studio/internal/ai/providers"
)

// TestGenerateFromDiagram_NonVisionProviderErrors — the default Ollama
// provider doesn't implement VisionUser, so the bridge must refuse
// before firing a bad request at the wire.
func TestGenerateFromDiagram_NonVisionProviderErrors(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "ignored") // Ollama
	_, _, err := c.GenerateFromDiagram(context.Background(), TopologyRequest{
		Description: "x",
		Tool:        "terraform",
		Provider:    "aws",
	}, []DiagramImage{{MediaType: "image/png", Data: []byte("data")}})
	if err == nil || !strings.Contains(err.Error(), "does not support vision") {
		t.Errorf("want vision-not-supported error, got %v", err)
	}
}

// TestGenerateFromDiagram_RejectsZeroImages — the text-only path lives
// on GenerateTopology; GenerateFromDiagram without an image is almost
// certainly a caller bug, so we surface it rather than silently
// degrading.
func TestGenerateFromDiagram_RejectsZeroImages(t *testing.T) {
	// Swap the provider for a stub that would succeed if called.
	c := NewClient("http://127.0.0.1:1", "ignored")
	c.provider = &stubVisionProvider{}
	c.providerErr = nil

	_, _, err := c.GenerateFromDiagram(context.Background(), TopologyRequest{}, nil)
	if err == nil || !strings.Contains(err.Error(), "no images") {
		t.Errorf("want no-images error, got %v", err)
	}
}

// TestGenerateFromDiagram_SuccessPath — stub VisionUser returns a JSON
// topology payload; the bridge parses it via parseAIResponse and
// surfaces resources.
func TestGenerateFromDiagram_SuccessPath(t *testing.T) {
	c := NewClient("http://127.0.0.1:1", "ignored")
	stub := &stubVisionProvider{
		reply: `{"message":"parsed vpc + subnet","resources":[{"type":"aws_vpc","name":"main","properties":{"cidr_block":"10.0.0.0/16"}}]}`,
	}
	c.provider = stub
	c.providerErr = nil

	msg, resources, err := c.GenerateFromDiagram(context.Background(), TopologyRequest{
		Tool:     "terraform",
		Provider: "aws",
	}, []DiagramImage{{MediaType: "image/png", Data: []byte{0x89, 0x50, 0x4e, 0x47}}})
	if err != nil {
		t.Fatalf("GenerateFromDiagram: %v", err)
	}
	if !strings.Contains(msg, "parsed") || len(resources) != 1 {
		t.Errorf("unexpected output: msg=%q resources=%+v", msg, resources)
	}
	if len(stub.lastImages) != 1 || stub.lastImages[0].MediaType != "image/png" {
		t.Errorf("provider didn't receive the image: %+v", stub.lastImages)
	}
}

// stubVisionProvider implements providers.Provider + VisionUser so the
// bridge tests exercise the typed-assertion branch without touching
// the real Anthropic transport. Minimal surface — only the methods
// GenerateFromDiagram actually invokes.
type stubVisionProvider struct {
	reply      string
	lastImages []providers.Image
}

func (s *stubVisionProvider) Kind() providers.Kind { return providers.KindAnthropic }
func (s *stubVisionProvider) Complete(context.Context, providers.Request) (string, error) {
	return s.reply, nil
}
func (s *stubVisionProvider) Stream(context.Context, providers.Request, providers.DeltaFunc) (string, error) {
	return s.reply, nil
}
func (s *stubVisionProvider) CompleteWithImages(_ context.Context, _ providers.Request, images []providers.Image) (string, error) {
	s.lastImages = images
	return s.reply, nil
}
