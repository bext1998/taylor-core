package config

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type layerConfig struct {
	Mode    *string `json:"mode"`
	ModelID *string `json:"model_id"`
}

var allowedFields = map[string]struct{}{
	"mode":     {},
	"model_id": {},
}

var forbiddenTerms = []string{
	"credential", "apikey", "api_key", "token", "password", "secret", "authorization",
	"safety", "approval", "grant", "policy",
}

func (l *Loader) Load(ctx context.Context, overrides CLIOverrides) (Resolved, error) {
	if strings.TrimSpace(l.WorkspaceRoot) == "" {
		return Resolved{}, configError(ErrConfigInvalid.Code, "project", errors.New("workspace root is required"))
	}
	root, err := filepath.Abs(l.WorkspaceRoot)
	if err != nil {
		return Resolved{}, configError(ErrConfigInvalid.Code, "project", errors.New("invalid workspace root"))
	}
	profile := l.UserProfile
	if strings.TrimSpace(profile) == "" {
		profile, err = os.UserHomeDir()
		if err != nil {
			return Resolved{}, configError(ErrConfigInvalid.Code, "global", errors.New("user profile is unavailable"))
		}
	}

	config := l.Defaults
	if config.Mode == "" {
		config.Mode = ModeWorkspace
	}
	if err := validateMode(config.Mode); err != nil {
		return Resolved{}, configError(ErrConfigInvalid.Code, "defaults", err)
	}
	for _, layer := range []struct {
		path   string
		source string
	}{
		{filepath.Join(profile, ".brunel", "config.json"), "global"},
		{filepath.Join(root, ".brunel", "config.json"), "project"},
	} {
		layerValues, present, err := readLayer(layer.path, layer.source)
		if err != nil {
			return Resolved{}, err
		}
		if present {
			applyLayer(&config, layerValues)
			if err := validateMode(config.Mode); err != nil {
				return Resolved{}, configError(ErrConfigInvalid.Code, layer.source, err)
			}
		}
	}
	if err := applyOverrides(&config, overrides); err != nil {
		return Resolved{}, err
	}

	credentials := l.Credentials
	if credentials == nil {
		credentials = NewPlatformCredentialSource()
	}
	// The OpenRouter key is optional here (see ErrCredentialNotFound): a
	// missing key resolves to an empty OpenRouterAPIKey, and the caller
	// decides whether the provider it launches needs one.
	key, err := credentials.OpenRouterAPIKey(ctx)
	if err != nil {
		if errors.Is(err, ErrCredentialNotFound) {
			return Resolved{Config: config}, nil
		}
		if errors.Is(err, ErrUnsupportedPlatform) {
			return Resolved{}, configError(ErrUnsupportedPlatform.Code, "credential-manager", err)
		}
		if errors.Is(err, ErrCredentialInvalid) {
			return Resolved{}, &Error{
				Code:    ErrConfigCredential.Code,
				Source:  "credential-manager",
				Message: `the stored OpenRouter key is not readable text; save it again with "brunel login"`,
				Cause:   err,
			}
		}
		return Resolved{}, configError(ErrConfigCredential.Code, "credential-manager", errors.New("OpenRouter credential unavailable"))
	}
	if strings.TrimSpace(key) == "" {
		return Resolved{Config: config}, nil
	}
	return Resolved{Config: config, apiKey: key}, nil
}

func readLayer(path, source string) (layerConfig, bool, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return layerConfig{}, false, nil
	}
	if err != nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("cannot read config file"))
	}
	defer file.Close()

	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&raw); err != nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("malformed JSON"))
	}
	if raw == nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("config must be a JSON object"))
	}
	if err := ensureEOF(decoder); err != nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("trailing JSON data"))
	}
	fields := make([]string, 0, len(raw))
	for field := range raw {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if _, ok := allowedFields[field]; ok {
			if string(raw[field]) == "null" {
				return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("null values are not allowed"))
			}
			continue
		}
		if isForbiddenField(field) {
			return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("forbidden security field"))
		}
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("unknown field"))
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("invalid config object"))
	}
	var values layerConfig
	decoder = json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&values); err != nil {
		return layerConfig{}, false, configError(ErrConfigInvalid.Code, source, errors.New("invalid config value"))
	}
	return values, true, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("extra JSON value")
		}
		return err
	}
	return nil
}

func isForbiddenField(field string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(field, "-", "_"))
	for _, term := range forbiddenTerms {
		if strings.Contains(normalized, term) {
			return true
		}
	}
	return false
}

func applyLayer(config *Config, layer layerConfig) {
	if layer.Mode != nil {
		config.Mode = *layer.Mode
	}
	if layer.ModelID != nil {
		config.ModelID = *layer.ModelID
	}
}

func applyOverrides(config *Config, overrides CLIOverrides) error {
	if overrides.Mode != nil {
		if strings.TrimSpace(*overrides.Mode) == "" {
			return configError(ErrConfigInvalid.Code, "cli", errors.New("mode override is empty"))
		}
		config.Mode = *overrides.Mode
	}
	if overrides.ModelID != nil {
		if strings.TrimSpace(*overrides.ModelID) == "" {
			return configError(ErrConfigInvalid.Code, "cli", errors.New("model override is empty"))
		}
		config.ModelID = *overrides.ModelID
	}
	if err := validateMode(config.Mode); err != nil {
		return configError(ErrConfigInvalid.Code, "cli", err)
	}
	return nil
}

func validateMode(mode string) error {
	switch mode {
	case ModeWorkspace, ModeReadonly, ModeBenchmark:
		return nil
	default:
		return errors.New("invalid mode")
	}
}
