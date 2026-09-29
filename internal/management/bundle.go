package management

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"

	"golang.org/x/crypto/scrypt"
)

const bundleLimit = 1 << 20
const bundleAAD = "fntv-relay-media-bundle:v1:scrypt-32768-8-1:aes-256-gcm"

type bundleEnvelope struct {
	Version    int    `json:"version"`
	Salt       []byte `json:"salt"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}
type bundlePayload struct {
	Version int               `json:"version"`
	Config  Config            `json:"config"`
	Keys    map[string]string `json:"keys"`
}

// MediaBundle keeps key material private. Config is an editable media-role
// draft, not an applied deployment. ImportKeys never replaces different keys.
type MediaBundle struct{ payload bundlePayload }

func (b *MediaBundle) Config() Config {
	raw, _ := json.Marshal(b.payload.Config)
	var copy Config
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func (b *MediaBundle) ImportKeys(keys *KeyStore) error { return keys.importKeys(b.payload.Keys) }

func bundleCipher(password string, salt []byte) (cipher.AEAD, error) {
	if len(password) < 12 || len(password) > 1024 {
		return nil, errors.New("package password must contain 12 to 1024 bytes")
	}
	key, err := scrypt.Key([]byte(password), salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, errors.New("could not derive package encryption key")
	}
	block, err := aes.NewCipher(key)
	for i := range key {
		key[i] = 0
	}
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// ExportBundle only includes enabled services' source restrictions and shared
// keys. Login URLs/hosts, credentials, certificates and STRM paths are omitted.
// The media listener starts at 49967; users must review local port mapping.
func ExportBundle(config Config, keys *KeyStore, password string) ([]byte, error) {
	if config.Mode != "split" || config.Role != "proxy" {
		return nil, errors.New("only split proxy configurations export media packages")
	}
	if err := config.Validate(); err != nil {
		return nil, err
	}
	media := Config{Version: 1, Mode: "split", Role: "media", Listen: Listener{Address: "0.0.0.0", Port: 49967}, PublicBaseURL: config.PublicBaseURL, Services: map[string]ServiceConfig{}}
	ids := []string{}
	for id, s := range config.Services {
		if s.Enabled {
			ids = append(ids, id)
			media.Services[id] = ServiceConfig{Enabled: true, AllowedUpstreams: append([]string(nil), s.AllowedUpstreams...)}
		}
	}
	material, err := keys.Material(ids)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(bundlePayload{Version: 1, Config: media, Keys: material})
	if err != nil {
		return nil, err
	}
	if len(raw) > bundleLimit/2 {
		return nil, errors.New("package is too large")
	}
	envelope := bundleEnvelope{Version: 1, Salt: make([]byte, 16)}
	if _, err = rand.Read(envelope.Salt); err != nil {
		return nil, err
	}
	aead, err := bundleCipher(password, envelope.Salt)
	if err != nil {
		return nil, err
	}
	envelope.Nonce = make([]byte, aead.NonceSize())
	if _, err = rand.Read(envelope.Nonce); err != nil {
		return nil, err
	}
	envelope.Ciphertext = aead.Seal(nil, envelope.Nonce, raw, []byte(bundleAAD))
	return json.Marshal(envelope)
}

func strictBundleJSON(raw []byte, v interface{}) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errors.New("invalid package format")
	}
	var extra interface{}
	if d.Decode(&extra) != io.EOF {
		return errors.New("invalid package format")
	}
	return nil
}

func OpenBundle(raw []byte, password string) (*MediaBundle, error) {
	if len(raw) == 0 || len(raw) > bundleLimit {
		return nil, errors.New("package size is invalid")
	}
	var envelope bundleEnvelope
	if err := strictBundleJSON(raw, &envelope); err != nil {
		return nil, err
	}
	if envelope.Version != 1 || len(envelope.Salt) != 16 || len(envelope.Nonce) != 12 || len(envelope.Ciphertext) < 16 {
		return nil, errors.New("unsupported or invalid package")
	}
	aead, err := bundleCipher(password, envelope.Salt)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, []byte(bundleAAD))
	if err != nil {
		return nil, errors.New("package password is incorrect or package was modified")
	}
	var payload bundlePayload
	if err := strictBundleJSON(plain, &payload); err != nil {
		return nil, err
	}
	if payload.Version != 1 || payload.Config.Mode != "split" || payload.Config.Role != "media" {
		return nil, errors.New("package must contain a version 1 split media configuration")
	}
	if err := payload.Config.Validate(); err != nil {
		return nil, errors.New("package media configuration is invalid")
	}
	count := 0
	for id, s := range payload.Config.Services {
		if !s.Enabled {
			return nil, errors.New("package contains an unselected service")
		}
		key, ok := payload.Keys[id]
		decoded, e := hex.DecodeString(key)
		if !ok || e != nil || len(decoded) != 32 {
			return nil, errors.New("package shared keys are incomplete")
		}
		count++
	}
	if len(payload.Keys) != count {
		return nil, errors.New("package contains unrelated keys")
	}
	return &MediaBundle{payload: payload}, nil
}
