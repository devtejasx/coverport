package metadata

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestDecodeBase64Payload(t *testing.T) {
	const plain = `{"predicate":{"buildConfig":{"tasks":[]}}}`
	padded := base64.StdEncoding.EncodeToString([]byte(plain))
	unpadded := base64.RawStdEncoding.EncodeToString([]byte(plain))

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "padded standard encoding",
			input: padded,
			want:  plain,
		},
		{
			// base64(1) accepts input without padding, so this has to keep working.
			name:  "unpadded standard encoding",
			input: unpadded,
			want:  plain,
		},
		{
			// base64(1) ignores line breaks, which is how wrapped output arrives.
			name:  "wrapped across lines",
			input: padded[:8] + "\n" + padded[8:16] + "\r\n" + padded[16:],
			want:  plain,
		},
		{
			name:  "surrounding whitespace",
			input: "  " + padded + "\n",
			want:  plain,
		},
		{
			name:    "not base64 at all",
			input:   "this is not base64!!",
			wantErr: true,
		},
		{
			name:    "empty input decodes to nothing",
			input:   "",
			want:    "",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := decodeBase64Payload(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", string(got))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("expected %q, got %q", tt.want, string(got))
			}
		})
	}
}

func TestDecodeBase64PayloadProducesParsableJSON(t *testing.T) {
	const plain = `{"predicate":{"buildConfig":{"tasks":[{"invocation":{}}]}}}`
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	decoded, err := decodeBase64Payload(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("decoded payload is not valid JSON: %v", err)
	}
	if _, ok := payload["predicate"]; !ok {
		t.Error("expected a predicate key in the decoded payload")
	}
}

// The decode used to run base64(1). That made metadata extraction fail wherever
// the binary is missing, which an in-process decode cannot.
func TestDecodeBase64PayloadNeedsNoExternalBinary(t *testing.T) {
	const plain = `{"predicate":{}}`
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	t.Setenv("PATH", t.TempDir())

	if _, err := exec.LookPath("base64"); err == nil {
		t.Skip("base64 is still resolvable with an empty PATH on this platform")
	}

	cmd := exec.Command("base64", "-d")
	cmd.Stdin = strings.NewReader(encoded)
	if _, err := cmd.Output(); err == nil {
		t.Skip("base64 ran despite an empty PATH; cannot demonstrate the old failure")
	}

	got, err := decodeBase64Payload(encoded)
	if err != nil {
		t.Fatalf("decoding must not depend on an external binary: %v", err)
	}
	if string(got) != plain {
		t.Errorf("expected %q, got %q", plain, string(got))
	}
}

func TestDecodeBase64PayloadHandlesRealisticEnvelope(t *testing.T) {
	// Shape of the payload cosign hands back for a Konflux build.
	const plain = `{"predicate":{"buildConfig":{"tasks":[{"invocation":{"environment":{"annotations":{"pipelinesascode.tekton.dev/repo-url":"https://example.com/org/repo","build.appstudio.redhat.com/commit_sha":"deadbeef"}}}}]}}}`
	encoded := base64.StdEncoding.EncodeToString([]byte(plain))

	decoded, err := decodeBase64Payload(encoded)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(decoded, &payload); err != nil {
		t.Fatalf("decoded payload is not valid JSON: %v", err)
	}

	predicate := payload["predicate"].(map[string]interface{})
	buildConfig := predicate["buildConfig"].(map[string]interface{})
	tasks := buildConfig["tasks"].([]interface{})
	first := tasks[0].(map[string]interface{})
	invocation := first["invocation"].(map[string]interface{})
	environment := invocation["environment"].(map[string]interface{})
	annotations := environment["annotations"].(map[string]interface{})

	if annotations["build.appstudio.redhat.com/commit_sha"] != "deadbeef" {
		t.Errorf("expected the commit sha to survive decoding, got %v", annotations["build.appstudio.redhat.com/commit_sha"])
	}

	if os.Getenv("COVERPORT_DEBUG") != "" {
		t.Logf("decoded: %s", decoded)
	}
}
