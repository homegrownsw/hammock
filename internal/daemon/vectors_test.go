package daemon

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// clientVectors are what the phone apps do that the daemon checks, the same
// in Swift (the iPhone) and Kotlin (Android) as here: what a device signs to
// sign in, a pairing link, a sealed notification, what a phone signs to
// approve the Hammock Key. testdata/client/vectors.json holds them; the
// iPhone's (apple/Tests/HammockKitTests/VectorsTests.swift) and Android's
// (android/client/src/test/…/VectorsTest.kt) tests read the same file, so a
// change to one side that the others don't follow fails somewhere.
// HMK_TEST_UPDATE_VECTORS=1 writes it afresh (a push note's nonce is random).
type clientVectors struct {
	Auth []struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	} `json:"auth"`
	Pairing []struct {
		Link          string   `json:"link"`
		HostName      string   `json:"host_name"`
		Addresses     []string `json:"addresses"`
		LanSubnets    []string `json:"lan_subnets"`
		CertSHA256    string   `json:"cert_sha256"`
		Secret        string   `json:"secret_hex"`
		RemoteEnabled bool     `json:"remote_enabled"`
	} `json:"pairing"`
	Push []struct {
		Host   string          `json:"host"`
		Key    string          `json:"key_hex"`
		Sealed string          `json:"sealed_b64"`
		Note   json.RawMessage `json:"note"`
	} `json:"push"`
	Approval []struct {
		KeyID    string `json:"key_id"`
		ID       string `json:"id"`
		DataHash string `json:"data_hash"`
		PaneID   string `json:"pane_id"`
		Window   bool   `json:"window"`
		Expires  int64  `json:"expires_unix_ms"`
		Message  string `json:"message"`
	} `json:"approval"`
}

var vectorsPath = filepath.Join("..", "..", "testdata", "client", "vectors.json")

func TestClientVectors(t *testing.T) {
	if os.Getenv("HMK_TEST_UPDATE_VECTORS") == "1" {
		writeClientVectors(t)
	}
	b, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var v clientVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Auth) == 0 || len(v.Pairing) == 0 || len(v.Push) == 0 || len(v.Approval) == 0 {
		t.Fatal("a kind of vector is missing")
	}
	for _, a := range v.Auth {
		nonce, _ := hex.DecodeString(a.Nonce)
		if got := hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, a.Cert, nonce)); got != a.Message {
			t.Errorf("auth message for %s: %s, want %s", a.Cert, got, a.Message)
		}
	}
	for _, p := range v.Pairing {
		raw, err := base64.RawURLEncoding.DecodeString(p.Link[len("hammock://pair/"):])
		if err != nil {
			t.Fatal(err)
		}
		var got pb.Pairing
		if err := proto.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.HostName != p.HostName || got.CertSha256 != p.CertSHA256 || hex.EncodeToString(got.Secret) != p.Secret ||
			len(got.Addresses) != len(p.Addresses) || got.RemoteEnabled != p.RemoteEnabled {
			t.Errorf("pairing %s decodes to %v", p.Link, &got)
		}
	}
	for _, p := range v.Push {
		key, _ := hex.DecodeString(p.Key)
		sealed, _ := base64.StdEncoding.DecodeString(p.Sealed)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatalf("push note for %s: %v", p.Host, err)
		}
		var a, b any
		_ = json.Unmarshal(plain, &a)
		_ = json.Unmarshal(p.Note, &b)
		if ja, _ := json.Marshal(a); string(ja) != string(mustJSON(b)) {
			t.Errorf("push note opens to %s, want %s", plain, p.Note)
		}
	}
	for _, a := range v.Approval {
		scope := "once"
		if a.Window {
			scope = "window"
		}
		if got := string(approvalMessage(a.KeyID, a.ID, a.DataHash, scope, a.PaneID, a.Expires)); got != a.Message {
			t.Errorf("approval message %q, want %q", got, a.Message)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// writeClientVectors makes the file from made-up values (test data only).
func writeClientVectors(t *testing.T) {
	var v clientVectors
	cert := "9f2c4e1a7b3d5c8e0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70"
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i * 7)
	}
	v.Auth = append(v.Auth, struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	}{cert, hex.EncodeToString(nonce), hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, cert, nonce))})
	// The pin is lowercased before it's signed, whatever case it came in.
	upper := "9F2C4E1A7B3D5C8E0F1A2B3C4D5E6F708192A3B4C5D6E7F8091A2B3C4D5E6F70"
	v.Auth = append(v.Auth, struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	}{upper, hex.EncodeToString(nonce), hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, upper, nonce))})

	secret := make([]byte, 24)
	for i := range secret {
		secret[i] = byte(200 - i)
	}
	p := &pb.Pairing{HostName: "Studio.local", Addresses: []string{"100.64.0.2:7433", "192.168.1.20:7433"}, LanSubnets: []string{"192.168.1.0/24"},
		CertSha256: cert, Secret: secret, ExpiresUnixMs: 1_800_000_000_000, RemoteEnabled: true}
	raw, _ := proto.Marshal(p)
	v.Pairing = append(v.Pairing, struct {
		Link          string   `json:"link"`
		HostName      string   `json:"host_name"`
		Addresses     []string `json:"addresses"`
		LanSubnets    []string `json:"lan_subnets"`
		CertSHA256    string   `json:"cert_sha256"`
		Secret        string   `json:"secret_hex"`
		RemoteEnabled bool     `json:"remote_enabled"`
	}{"hammock://pair/" + base64.RawURLEncoding.EncodeToString(raw), p.HostName, p.Addresses, p.LanSubnets, cert, hex.EncodeToString(secret), true})

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	note := pushNote{Title: "Claude needs you", Subtitle: "Web ▸ api", Body: "Allow Bash: npm test?", Category: "grant", Grant: "gr_1",
		Host: cert, ID: "c_1", At: 1_800_000_000, Live: true}
	plain, _ := json.Marshal(note)
	sealed, err := seal(key, plain)
	if err != nil {
		t.Fatal(err)
	}
	v.Push = append(v.Push, struct {
		Host   string          `json:"host"`
		Key    string          `json:"key_hex"`
		Sealed string          `json:"sealed_b64"`
		Note   json.RawMessage `json:"note"`
	}{cert, hex.EncodeToString(key), base64.StdEncoding.EncodeToString(sealed), plain})

	for _, w := range []bool{true, false} {
		scope := "once"
		if w {
			scope = "window"
		}
		v.Approval = append(v.Approval, struct {
			KeyID    string `json:"key_id"`
			ID       string `json:"id"`
			DataHash string `json:"data_hash"`
			PaneID   string `json:"pane_id"`
			Window   bool   `json:"window"`
			Expires  int64  `json:"expires_unix_ms"`
			Message  string `json:"message"`
		}{"k1", "fk_1", "ab", "pn_1", w, 123, string(approvalMessage("k1", "fk_1", "ab", scope, "pn_1", 123))})
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
